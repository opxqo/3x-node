package main

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/opxqo/3x-node/v3/internal/node"
)

// Two-level pane: a selectable inbound list that Enter descends into and Esc
// climbs back out of, one level at a time.
type inboundBrowser struct {
	inbounds []node.Inbound
	cursor   int
	offset   int
	detail   []string
}

func newInboundBrowser(c node.Config) (*inboundBrowser, error) {
	inbounds, err := loadInbounds(c)
	if err != nil {
		return nil, err
	}
	return &inboundBrowser{inbounds: inbounds}, nil
}

// The renderer highlights the row carrying this marker, so only the cursor row
// may start with it.
const inboundCursor = "▸"

func (b *inboundBrowser) title() string {
	if b.detail == nil {
		return "入站列表"
	}
	in := b.inbounds[b.cursor]
	return fmt.Sprintf("入站 #%d · %s", in.ID, in.Tag)
}

func (b *inboundBrowser) lines() []string {
	if b.detail != nil {
		return b.detail
	}
	if len(b.inbounds) == 0 {
		return []string{"没有已配置的入站。"}
	}
	rows := []string{inboundRow(" ", "ID", "端口", "协议", "传输/安全", "状态", "标签")}
	for i, in := range b.inbounds {
		marker := " "
		if i == b.cursor {
			marker = inboundCursor
		}
		state := "停用"
		if in.Enable {
			state = "运行"
		}
		s := summarizeInbound(in)
		rows = append(rows, inboundRow(marker, strconv.Itoa(in.ID), strconv.Itoa(in.Port),
			in.Protocol, s.Network+"/"+s.Security, state, in.Tag))
	}
	return rows
}

func (b *inboundBrowser) move(delta int) bool {
	if b.detail != nil || len(b.inbounds) == 0 {
		return false
	}
	b.cursor = min(max(0, b.cursor+delta), len(b.inbounds)-1)
	return true
}

func (b *inboundBrowser) open() bool {
	if b.detail != nil || len(b.inbounds) == 0 {
		return false
	}
	b.detail = inboundDetail(b.inbounds[b.cursor])
	return true
}

func (b *inboundBrowser) back() bool {
	if b.detail == nil {
		return false
	}
	b.detail = nil
	return true
}

// Keys the browser declines fall through to the shared result-pane scrolling,
// which is what moves the detail view.
func (b *inboundBrowser) key(k byte) bool {
	switch k {
	case 'j':
		return b.move(1)
	case 'k':
		return b.move(-1)
	case '\r', '\n':
		return b.open()
	case 'q', 27:
		return b.back()
	}
	return false
}

// Keeps the cursor row inside the viewport with one row of context above it, so
// reaching the first inbound scrolls the column header back into view.
func (b *inboundBrowser) follow(page int) int {
	if b.detail != nil {
		return 0
	}
	row := b.cursor + 1
	b.offset = min(max(b.offset, row-page+1), max(0, row-1))
	return b.offset
}

// Padded by display width: a %-Ns verb counts bytes, which misaligns every
// column that follows a CJK state or tag.
func inboundRow(marker, id, port, protocol, transport, state, tag string) string {
	return marker + " " + inboundPad(id, 4) + " " + inboundPad(port, 7) + " " +
		inboundPad(protocol, 7) + " " + inboundPad(transport, 15) + " " +
		inboundPad(state, 6) + " " + tag
}

func inboundField(label, value string) string {
	if value == "" {
		value = "—"
	}
	return "  " + label + strings.Repeat(" ", max(1, 14-terminalDisplayWidth(label))) + value
}

func inboundList(label string, values []string) []string {
	if len(values) == 0 {
		return []string{inboundField(label, "")}
	}
	rows := make([]string, 0, len(values)/3+1)
	for i := 0; i < len(values); i += 3 {
		chunk := strings.Join(values[i:min(len(values), i+3)], ", ")
		if i == 0 {
			rows = append(rows, inboundField(label, chunk))
			continue
		}
		rows = append(rows, inboundField("", chunk))
	}
	return rows
}

func inboundPad(value string, width int) string {
	value = terminalClip(value, width)
	return value + strings.Repeat(" ", max(0, width-terminalDisplayWidth(value)))
}

func inboundDetail(in node.Inbound) []string {
	var stream struct {
		Network         string          `json:"network"`
		Security        string          `json:"security"`
		RealitySettings json.RawMessage `json:"realitySettings"`
		TLSSettings     json.RawMessage `json:"tlsSettings"`
	}
	_ = json.Unmarshal(in.StreamSettings, &stream)
	if stream.Network == "" {
		stream.Network = "tcp"
	}
	if stream.Security == "" {
		stream.Security = "none"
	}
	host := in.Listen
	if host == "" {
		host = "0.0.0.0"
	}
	state := "停用"
	if in.Enable {
		state = "运行"
	}
	total := "不限"
	if in.Total > 0 {
		total = bytes(uint64(in.Total))
	}
	lines := []string{
		"基本",
		inboundField("备注", in.Remark),
		inboundField("标签", in.Tag),
		inboundField("协议", in.Protocol),
		inboundField("监听", net.JoinHostPort(host, strconv.Itoa(in.Port))),
		inboundField("状态", state),
		inboundField("流量", fmt.Sprintf("↑ %s  ↓ %s  / %s", bytes(uint64(in.Up)), bytes(uint64(in.Down)), total)),
		"",
		"传输",
		inboundField("网络", stream.Network),
		inboundField("安全", stream.Security),
	}
	switch stream.Security {
	case "reality":
		lines = append(lines, "")
		lines = append(lines, realityDetail(stream.RealitySettings)...)
	case "tls":
		lines = append(lines, "")
		lines = append(lines, tlsDetail(stream.TLSSettings)...)
	}
	return append(lines, clientDetail(in)...)
}

// Xray authenticates REALITY with privateKey alone and never reads the stored
// settings.publicKey, so the pane derives the key clients must actually use.
func derivePublicKey(privateKey string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func realityDetail(raw json.RawMessage) []string {
	var r struct {
		Target       string   `json:"target"`
		Dest         string   `json:"dest"`
		ServerNames  []string `json:"serverNames"`
		PrivateKey   string   `json:"privateKey"`
		ShortIds     []string `json:"shortIds"`
		MinClientVer string   `json:"minClientVer"`
		MaxClientVer string   `json:"maxClientVer"`
		MaxTimediff  int64    `json:"maxTimediff"`
		Show         bool     `json:"show"`
		Settings     struct {
			PublicKey   string `json:"publicKey"`
			Fingerprint string `json:"fingerprint"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return []string{"REALITY", inboundField("解析失败", err.Error())}
	}
	target := r.Target
	if target == "" {
		target = r.Dest
	}
	lines := []string{"REALITY", inboundField("目标", target)}
	lines = append(lines, inboundList("SNI", r.ServerNames)...)
	lines = append(lines, inboundList("Short IDs", r.ShortIds)...)
	lines = append(lines,
		inboundField("指纹", r.Settings.Fingerprint),
		inboundField("私钥", r.PrivateKey))
	derived, err := derivePublicKey(r.PrivateKey)
	switch {
	case err != nil:
		lines = append(lines, inboundField("公钥", "无法由私钥推导: "+err.Error()))
	case derived == r.Settings.PublicKey:
		lines = append(lines, inboundField("公钥", derived))
	default:
		lines = append(lines,
			inboundField("公钥", derived),
			inboundField("", "⚠ 配置里存的是 "+valueOrDash(r.Settings.PublicKey)),
			inboundField("", "  与私钥不匹配，客户端 pbk 请用上面那个"))
	}
	if r.MinClientVer != "" || r.MaxClientVer != "" {
		lines = append(lines, inboundField("客户端版本",
			valueOrDash(r.MinClientVer)+" ~ "+valueOrDash(r.MaxClientVer)))
	}
	if r.MaxTimediff > 0 {
		lines = append(lines, inboundField("时间差", fmt.Sprintf("%d ms", r.MaxTimediff)))
	}
	if r.Show {
		lines = append(lines, inboundField("调试", "show 已开启，握手细节写入 Xray 日志"))
	}
	return lines
}

func valueOrDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func tlsDetail(raw json.RawMessage) []string {
	var t struct {
		ServerName   string   `json:"serverName"`
		ALPN         []string `json:"alpn"`
		Certificates []struct {
			CertificateFile string `json:"certificateFile"`
		} `json:"certificates"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return []string{"TLS", inboundField("解析失败", err.Error())}
	}
	lines := []string{"TLS", inboundField("SNI", t.ServerName)}
	lines = append(lines, inboundList("ALPN", t.ALPN)...)
	for _, c := range t.Certificates {
		lines = append(lines, inboundField("证书", c.CertificateFile))
	}
	return lines
}

func clientDetail(in node.Inbound) []string {
	clients, err := in.Clients()
	if err != nil {
		return []string{"", "客户端", inboundField("解析失败", err.Error())}
	}
	stats := make(map[string]node.Traffic, len(in.ClientStats))
	for _, s := range in.ClientStats {
		stats[s.Email] = s
	}
	lines := []string{"", fmt.Sprintf("客户端 (%d)", len(clients))}
	if len(clients) == 0 {
		return append(lines, "  没有客户端。")
	}
	for _, c := range clients {
		email := c.Text("email")
		state := "停用"
		if c.Enabled() {
			state = "启用"
		}
		s := stats[email]
		lines = append(lines, fmt.Sprintf("  %s %s ↑ %s  ↓ %s",
			inboundPad(email, 22), inboundPad(state, 6), bytes(uint64(s.Up)), bytes(uint64(s.Down))))
		lines = append(lines, fmt.Sprintf("    %s  flow=%s",
			c.Text("id"), valueOrDash(c.Text("flow"))))
	}
	return lines
}
