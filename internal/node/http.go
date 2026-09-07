package node

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Envelope struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     any    `json:"obj"`
}

func reply(w http.ResponseWriter, code int, obj any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	e := Envelope{Success: err == nil, Obj: obj}
	if err != nil {
		e.Msg = err.Error()
	}
	_ = json.NewEncoder(w).Encode(e)
}

func (n *Node) Handler() http.Handler {
	mux := &routeMux{}
	prefix := n.Config.BasePath + "panel/api/"
	add := func(method, path string, fn func(*http.Request) (any, error)) {
		mux.HandleFunc(method+" "+prefix+path, func(w http.ResponseWriter, r *http.Request) { obj, err := fn(r); reply(w, 200, obj, err) })
	}
	add("GET", "server/status", func(_ *http.Request) (any, error) { return n.Status(), nil })
	add("GET", "inbounds/list", func(_ *http.Request) (any, error) { return n.Inbounds(), nil })
	for _, path := range []string{"hosts/list", "server/descendants", "server/clientIps"} {
		add("GET", path, func(_ *http.Request) (any, error) { return []any{}, nil })
	}
	add("POST", "server/clientIps", func(r *http.Request) (any, error) { _, err := readBody(r); return nil, err })
	add("POST", "clients/clientIpsByGuid", func(_ *http.Request) (any, error) { return map[string]any{}, nil })
	for _, kind := range []string{"onlines", "onlinesByGuid", "activeInbounds", "lastOnline"} {
		add("POST", "clients/"+kind, func(_ *http.Request) (any, error) { return n.Online(kind), nil })
	}
	add("GET", "server/getWebCertFiles", func(_ *http.Request) (any, error) {
		return map[string]string{"webCertFile": n.Config.CertFile, "webKeyFile": n.Config.KeyFile}, nil
	})
	add("POST", "server/restartXrayService", func(_ *http.Request) (any, error) { return n.Restart() })
	add("POST", "server/updatePanel", func(_ *http.Request) (any, error) {
		return nil, errors.New("3x-ui-node requires its own verified package; full-panel updater is disabled")
	})
	add("POST", "inbounds/add", func(r *http.Request) (any, error) {
		ib, err := decodeInbound(r)
		if err != nil {
			return nil, err
		}
		return n.PutInbound(ib, 0)
	})
	add("POST", "inbounds/update/{id}", func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		ib, err := decodeInbound(r)
		if err != nil {
			return nil, err
		}
		return n.PutInbound(ib, id)
	})
	add("POST", "inbounds/del/{id}", func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return n.DeleteInbound(id)
	})
	add("POST", "inbounds/{id}/subSortIndex", func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		var b struct {
			Index int `json:"subSortIndex"`
		}
		if err = decode(r, &b); err != nil {
			return nil, err
		}
		if b.Index < 1 {
			return nil, errors.New("subSortIndex must be positive")
		}
		return n.transaction(func(s *State) (any, error) {
			ib := s.Find(id)
			if ib == nil {
				return nil, errors.New("inbound not found")
			}
			ib.SubSortIndex = b.Index
			return nil, nil
		})
	})
	add("POST", "clients/add", func(r *http.Request) (any, error) {
		var b struct {
			Client Client `json:"client"`
			IDs    []int  `json:"inboundIds"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return n.ChangeClient("", b.Client, b.IDs, "add")
	})
	add("POST", "clients/update/{email}", func(r *http.Request) (any, error) {
		var c Client
		if err := decode(r, &c); err != nil {
			return nil, err
		}
		ids, err := queryIDs(r)
		if err != nil {
			return nil, err
		}
		return n.ChangeClient(r.PathValue("email"), c, ids, "update")
	})
	add("POST", "clients/del/{email}", func(r *http.Request) (any, error) { return n.ChangeClient(r.PathValue("email"), nil, nil, "delete") })
	add("POST", "clients/{email}/detach", func(r *http.Request) (any, error) {
		var b struct {
			IDs []int `json:"inboundIds"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return n.ChangeClient(r.PathValue("email"), nil, b.IDs, "detach")
	})
	add("POST", "clients/resetTraffic/{email}", func(r *http.Request) (any, error) { return n.ResetTraffic(r.PathValue("email"), 0) })
	add("POST", "inbounds/resetAllTraffics", func(_ *http.Request) (any, error) { return n.ResetTraffic("", 0) })
	add("POST", "inbounds/{id}/resetTraffic", func(r *http.Request) (any, error) {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		return n.ResetTraffic("", id)
	})
	add("POST", "inbounds/pushClientTraffics", func(r *http.Request) (any, error) {
		var b struct {
			Master string    `json:"masterGuid"`
			Rows   []Traffic `json:"traffics"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return n.PushGlobals(b.Master, b.Rows)
	})
	sem := make(chan struct{}, 4)
	want := sha256.Sum256([]byte(n.Config.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(tok))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			reply(w, 401, nil, errors.New("unauthorized"))
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			reply(w, 503, nil, errors.New("node busy"))
			return
		}
		if r.ContentLength > MaxBody {
			reply(w, 413, nil, errors.New("request exceeds 1 MiB"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
		if enc := r.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
			reply(w, 415, nil, errors.New("compression not negotiated"))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Existing Gin routes overlap under ServeMux's wildcard conflict rules.
type routeMux struct{ routes []nodeRoute }
type nodeRoute struct {
	method  string
	parts   []string
	handler http.HandlerFunc
}

func (m *routeMux) HandleFunc(pattern string, h http.HandlerFunc) {
	method, path, _ := strings.Cut(pattern, " ")
	m.routes = append(m.routes, nodeRoute{method: method, parts: strings.Split(path, "/"), handler: h})
}
func (m *routeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.EscapedPath(), "/")
	for _, route := range m.routes {
		if route.method != r.Method || len(parts) != len(route.parts) {
			continue
		}
		params := map[string]string{}
		match := true
		for j, part := range route.parts {
			value, err := url.PathUnescape(parts[j])
			if err != nil {
				match = false
				break
			}
			if strings.HasPrefix(part, "{") {
				name := strings.Trim(part, "{}")
				if name == "id" {
					if id, err := strconv.Atoi(value); err != nil || id < 1 {
						match = false
						break
					}
				}
				params[name] = value
			} else if part != value {
				match = false
				break
			}
		}
		if match {
			for k, v := range params {
				r.SetPathValue(k, v)
			}
			route.handler(w, r)
			return
		}
	}
	reply(w, 404, nil, errors.New("endpoint not supported by headless node"))
}

func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBody {
		return nil, errors.New("request exceeds 1 MiB")
	}
	if h := r.Header.Get("X-Config-Sha256"); h != "" {
		sum := sha256.Sum256(b)
		if h != hex.EncodeToString(sum[:]) {
			return nil, errors.New("config integrity mismatch")
		}
	}
	return b, nil
}

func decode(r *http.Request, out any) error {
	b, err := readBody(r)
	if err != nil {
		return err
	}
	typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if typ == "application/x-www-form-urlencoded" {
		form, err := url.ParseQuery(string(b))
		if err != nil {
			return err
		}
		obj := map[string]json.RawMessage{}
		for k, vs := range form {
			if len(vs) != 1 {
				return errors.New("duplicate form key")
			}
			v := vs[0]
			switch k {
			case "id", "port", "total", "expiryTime", "subSortIndex", "trafficResetDay":
				if _, err = strconv.ParseInt(v, 10, 64); err != nil {
					return err
				}
				obj[k] = json.RawMessage(v)
			case "enable", "disableFlow":
				if v != "true" && v != "false" {
					return errors.New("invalid boolean")
				}
				obj[k] = json.RawMessage(v)
			case "settings", "streamSettings", "sniffing":
				if v == "" {
					v = "{}"
				}
				if !json.Valid([]byte(v)) {
					return errors.New("invalid nested JSON")
				}
				obj[k] = json.RawMessage(v)
			default:
				obj[k], _ = json.Marshal(v)
			}
		}
		b, err = json.Marshal(obj)
		if err != nil {
			return err
		}
	} else if typ != "application/json" {
		return errors.New("expected application/json or form-urlencoded")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func decodeInbound(r *http.Request) (Inbound, error) {
	ib := Inbound{Enable: true, SubSortIndex: 1, TrafficReset: "never", ShareAddrStrategy: "node", Sniffing: Object(`{"enabled":false}`)}
	err := decode(r, &ib)
	ib.ClientStats = nil
	return ib, err
}
func pathID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		return 0, errors.New("invalid inbound id")
	}
	return id, nil
}
func queryIDs(r *http.Request) ([]int, error) {
	v := r.URL.Query().Get("inboundIds")
	if v == "" {
		return nil, nil
	}
	out := []int{}
	for _, s := range strings.Split(v, ",") {
		id, err := strconv.Atoi(s)
		if err != nil || id < 1 {
			return nil, errors.New("invalid inboundIds")
		}
		out = append(out, id)
	}
	return out, nil
}

type boundedListener struct {
	net.Listener
	slots chan struct{}
}
type boundedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *boundedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &boundedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

func (n *Node) Server() (*http.Server, net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(n.Config.CertFile, n.Config.KeyFile)
	if err != nil {
		return nil, nil, err
	}
	l, err := net.Listen("tcp", n.Config.Listen)
	if err != nil {
		return nil, nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	srv := &http.Server{Handler: n.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 8 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, TLSConfig: tlsConfig}
	return srv, tls.NewListener(&boundedListener{Listener: l, slots: make(chan struct{}, 16)}, tlsConfig), nil
}
