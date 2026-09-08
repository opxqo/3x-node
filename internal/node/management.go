package node

import (
	"net"
	"net/http"
	"time"
)

// Observations of authenticated socket peers, not a master identity or binding.
// Kept in memory to avoid writing state on every status poll.
type ManagementActivity struct {
	LastRequest int64  `json:"lastRequest"`
	RequestPeer string `json:"requestPeer"`
	LastConfig  int64  `json:"lastConfig"`
	ConfigPeer  string `json:"configPeer"`
}

func (n *Node) recordManagement(r *http.Request, route string, success bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now().Unix()
	n.management.LastRequest, n.management.RequestPeer = now, ip.String()
	if success && r.Method == http.MethodPost {
		switch route {
		case "inbounds/add", "inbounds/update/{id}", "inbounds/del/{id}", "inbounds/{id}/subSortIndex", "clients/add", "clients/update/{email}", "clients/del/{email}", "clients/{email}/detach":
			n.management.LastConfig, n.management.ConfigPeer = now, ip.String()
		}
	}
}
