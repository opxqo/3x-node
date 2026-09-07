package node

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func sharedCredentialClient(t *testing.T) Client {
	t.Helper()
	b, err := os.ReadFile("testdata/master_shared_credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []Client
	if err = json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs[0]
}

func assertSharedCredentialProjection(t *testing.T, n *Node) {
	t.Helper()
	c := clientByEmail(t, n.Inbounds()[0], "audit-client-0")
	for _, k := range []string{"password", "auth", "secret"} {
		if _, ok := c[k]; ok {
			t.Fatalf("retained unrelated credential %s", k)
		}
	}
	if c.Text("id") != "00000000-0000-4000-8000-000000000020" || !c.Enabled() {
		t.Fatal("VLESS identity changed")
	}
}

func TestSharedCredentialsHTTP(t *testing.T) {
	for _, op := range []string{"add", "update", "inbound_add", "inbound_update"} {
		t.Run(op, func(t *testing.T) {
			n, _ := testNode(t)
			c := sharedCredentialClient(t)
			var body any
			path := ""
			switch op {
			case "add":
				path = "clients/add"
				body = map[string]any{"client": c, "inboundIds": []int{1}}
			case "update":
				clean := cloneClient(c)
				for _, k := range []string{"password", "auth", "secret"} {
					delete(clean, k)
				}
				if _, err := n.ChangeClient("", clean, []int{1}, "add"); err != nil {
					t.Fatal(err)
				}
				path = "clients/update/audit-client-0?inboundIds=1"
				body = c
			default:
				ib := n.Inbounds()[0]
				ib.SetClients([]Client{c})
				body = ib
				path = "inbounds/add"
				if op == "inbound_update" {
					path = "inbounds/update/1"
				}
			}
			b, _ := json.Marshal(body)
			r := httptest.NewRequest("POST", "/panel/api/"+path, strings.NewReader(string(b)))
			r.Header.Set("Authorization", "Bearer "+n.Config.Token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			n.Handler().ServeHTTP(w, r)
			var res Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if !res.Success {
				t.Fatalf("%s: %s", path, res.Msg)
			}
			assertSharedCredentialProjection(t, n)
		})
	}
}

func TestSharedCredentialsStillRejectRestrictions(t *testing.T) {
	for _, key := range []string{"limitIp", "reset", "resetDay", "resetMax", "limitHwid", "hwidLimit", "deviceLimit", "reverse", "unexpected"} {
		t.Run(key, func(t *testing.T) {
			n, _ := testNode(t)
			c := sharedCredentialClient(t)
			c.Set(key, 1)
			before, _ := json.Marshal(n.Inbounds())

			if _, err := n.ChangeClient("", c, []int{1}, "add"); err == nil {
				t.Fatal("push accepted restriction")
			}
			after, _ := json.Marshal(n.Inbounds())
			if string(before) != string(after) {
				t.Fatal("failed operation changed clients")
			}
		})
	}
}

func TestSharedCredentialsPreserveUpdatesAndLimits(t *testing.T) {
	n, e := testNode(t)
	c := sharedCredentialClient(t)
	if _, err := n.ChangeClient("", c, []int{1}, "add"); err != nil {
		t.Fatal(err)
	}
	c.Set("id", "00000000-0000-4000-8000-000000000099")
	c.Set("enable", false)
	c.Set("totalGB", int64(1048576))
	c.Set("expiryTime", int64(4102444800000))
	c.Set("comment", "updated")
	if _, err := n.ChangeClient(c.Text("email"), c, []int{1}, "update"); err != nil {
		t.Fatal(err)
	}
	got := clientByEmail(t, n.Inbounds()[0], c.Text("email"))
	for _, key := range []string{"id", "enable", "totalGB", "expiryTime", "comment"} {
		if string(got[key]) != string(c[key]) {
			t.Fatalf("lost field %s", key)
		}
	}
	if _, ok := e.live[0].Users[c.Text("email")]; ok {
		t.Fatal("disabled user remains active")
	}
	c.Set("enable", true)
	if _, err := n.ChangeClient(c.Text("email"), c, []int{1}, "update"); err != nil {
		t.Fatal(err)
	}
	if e.live[0].Users[c.Text("email")].ID != c.Text("id") {
		t.Fatal("UUID rotation not applied")
	}
	for _, key := range []string{"password", "auth", "secret"} {
		if c.Text(key) == "" {
			t.Fatal("caller object mutated")
		}
	}
}

func cloneClient(c Client) Client {
	out := Client{}
	for k, v := range c {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
func clientByEmail(t *testing.T, ib Inbound, email string) Client {
	t.Helper()
	cs, err := ib.Clients()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Text("email") == email {
			return c
		}
	}
	t.Fatal("client missing")
	return nil
}
