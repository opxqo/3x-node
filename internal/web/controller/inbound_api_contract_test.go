package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

// TestNodeSyncCanReadFullInboundClients characterizes the source contract used
// by the future leaf puller. The production panel is exercised through the
// real API controller and the real node-sync token scope; the test does not
// recreate the response shape in a standalone fake handler.
func TestNodeSyncCanReadFullInboundClients(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	db := database.GetDB()
	var user model.User
	if err := db.First(&user).Error; err != nil {
		t.Fatalf("load seeded user: %v", err)
	}
	const token = "node-sync-contract-token"
	if err := db.Create(&model.ApiToken{
		Name:    "node-sync-contract",
		Token:   crypto.HashTokenSHA256(token),
		Enabled: true,
		Scope:   model.ApiScopeNodeSync,
	}).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	const uuid = "00000000-0000-4000-8000-000000000001"
	const settings = `{"clients":[{"id":"` + uuid + `","email":"leaf-contract","enable":true,"flow":"","totalGB":0,"expiryTime":0}],"decryption":"none","encryption":"none"}`
	if err := db.Create(&model.Inbound{
		UserId:         user.Id,
		Remark:         "contract-inbound",
		Enable:         true,
		TrafficReset:   "never",
		Port:           26700,
		Protocol:       model.VLESS,
		Settings:       settings,
		StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`,
		Sniffing:       `{"enabled":false}`,
		Tag:            "contract-in-26700",
	}).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	a := &APIController{}
	api := router.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	NewInboundController(api.Group("/inbounds"))
	NewClientController(api.Group("/clients"))

	req := httptest.NewRequest(http.MethodGet, "/panel/api/inbounds/list", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var envelope struct {
		Success bool              `json:"success"`
		Obj     []json.RawMessage `json:"obj"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list response: %v; body=%s", err, resp.Body.String())
	}
	if !envelope.Success || len(envelope.Obj) != 1 {
		t.Fatalf("unexpected list envelope: success=%v objects=%d body=%s", envelope.Success, len(envelope.Obj), resp.Body.String())
	}
	var inbound struct {
		ID       int `json:"id"`
		Settings struct {
			Clients []struct {
				ID     string `json:"id"`
				Email  string `json:"email"`
				Enable bool   `json:"enable"`
			} `json:"clients"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(envelope.Obj[0], &inbound); err != nil {
		t.Fatalf("decode inbound: %v", err)
	}
	if inbound.ID == 0 || len(inbound.Settings.Clients) != 1 {
		t.Fatalf("full client list missing: %+v; body=%s", inbound, resp.Body.String())
	}
	client := inbound.Settings.Clients[0]
	if client.ID != uuid || client.Email != "leaf-contract" || !client.Enable {
		t.Fatalf("client contract: %+v; body=%s", client, resp.Body.String())
	}

	for _, path := range []string{"/panel/api/inbounds/get/1", "/panel/api/clients/list"} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp = httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("node-sync %s status = %d, want 403; body=%s", path, resp.Code, resp.Body.String())
		}
	}

	if strings.Contains(resp.Body.String(), token) {
		t.Fatal("API response leaked the bearer token")
	}
}
