package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultMasterSyncInterval = 60
	MinMasterSyncInterval     = 30
	MaxMasterSyncInterval     = 3600
)

type MasterSyncMapping struct {
	ID              string `json:"id"`
	MasterInboundID int    `json:"masterInboundId"`
	LocalInboundID  int    `json:"localInboundId"`
}

type MasterSyncConfig struct {
	Enabled         bool                `json:"enabled"`
	BaseURL         string              `json:"baseURL"`
	TokenFile       string              `json:"tokenFile"`
	CertSHA256      string              `json:"certSHA256"`
	IntervalSeconds int                 `json:"intervalSeconds"`
	Mappings        []MasterSyncMapping `json:"mappings,omitempty"`
}

type Config struct {
	Listen             string           `json:"listen"`
	BasePath           string           `json:"basePath"`
	Token              string           `json:"token"`
	CertFile           string           `json:"certFile"`
	KeyFile            string           `json:"keyFile"`
	StateFile          string           `json:"stateFile"`
	XrayBinary         string           `json:"xrayBinary"`
	APIPort            int              `json:"xrayApiPort"`
	DefaultClientUUID  string           `json:"defaultClientUUID,omitempty"`
	DefaultClientEmail string           `json:"defaultClientEmail,omitempty"`
	MasterSync         MasterSyncConfig `json:"masterSync,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	_, p, err := net.SplitHostPort(c.Listen)
	if err != nil || p == "0" {
		return c, errors.New("listen must specify an address and nonzero port")
	}
	if len(c.Token) < 32 {
		return c, errors.New("token must contain at least 32 characters")
	}
	if c.BasePath == "" {
		c.BasePath = "/"
	}
	c.BasePath = "/" + strings.Trim(c.BasePath, "/") + "/"
	if c.BasePath == "//" {
		c.BasePath = "/"
	}
	if strings.Contains(c.BasePath, "..") || strings.ContainsAny(c.BasePath, "?#%") {
		return c, errors.New("invalid basePath")
	}
	for _, p := range []string{c.StateFile, c.XrayBinary, c.CertFile, c.KeyFile} {
		if !filepath.IsAbs(p) {
			return c, errors.New("state, binary and TLS paths must be absolute")
		}
	}
	if c.APIPort < 1024 || c.APIPort > 65535 {
		return c, errors.New("xrayApiPort must be 1024..65535")
	}
	if (c.DefaultClientUUID == "") != (c.DefaultClientEmail == "") {
		return c, errors.New("default client UUID and email must be configured together")
	}
	if err := ValidateDefaultClient(c.DefaultClientUUID, c.DefaultClientEmail); err != nil {
		return c, err
	}
	if err := ValidateMasterSync(c.MasterSync); err != nil {
		return c, err
	}
	return c, nil
}

func ValidateMasterSync(s MasterSyncConfig) error {
	if !s.Enabled {
		return nil
	}
	if s.BaseURL == "" {
		return errors.New("master sync baseURL is required")
	}
	u, err := url.Parse(s.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("master sync baseURL must be an https URL without credentials, query, or fragment")
	}
	if s.TokenFile == "" || !filepath.IsAbs(s.TokenFile) {
		return errors.New("master sync tokenFile must be an absolute path")
	}
	if s.IntervalSeconds == 0 {
		s.IntervalSeconds = DefaultMasterSyncInterval
	}
	if s.IntervalSeconds < MinMasterSyncInterval || s.IntervalSeconds > MaxMasterSyncInterval {
		return fmt.Errorf("master sync intervalSeconds must be %d..%d", MinMasterSyncInterval, MaxMasterSyncInterval)
	}
	if err := ValidateSHA256Fingerprint(s.CertSHA256); err != nil {
		return fmt.Errorf("master sync certSHA256: %w", err)
	}
	if len(s.Mappings) == 0 || len(s.Mappings) > MaxInbounds {
		return fmt.Errorf("master sync mappings must contain 1..%d entries", MaxInbounds)
	}
	seen := map[string]bool{}
	masters := map[int]bool{}
	locals := map[int]bool{}
	for _, m := range s.Mappings {
		if m.ID == "" || strings.ContainsAny(m.ID, "/\\\x00") || seen[m.ID] {
			return errors.New("master sync mapping IDs must be non-empty and unique")
		}
		if m.MasterInboundID < 1 || m.LocalInboundID < 1 || masters[m.MasterInboundID] || locals[m.LocalInboundID] {
			return errors.New("master sync inbound IDs must be positive and unique")
		}
		seen[m.ID], masters[m.MasterInboundID], locals[m.LocalInboundID] = true, true, true
	}
	return nil
}

// DefaultClient returns the leaf-only compatibility client injected into empty
// VLESS inbounds received from an unchanged master panel.
func (c Config) DefaultClient() Client {
	client := Client{}
	client.Set("id", c.DefaultClientUUID)
	client.Set("email", c.DefaultClientEmail)
	client.Set("enable", true)
	client.Set("flow", "")
	client.Set("totalGB", int64(0))
	client.Set("expiryTime", int64(0))
	return client
}

func ValidateDefaultClient(uuid, email string) error {
	if uuid == "" && email == "" {
		return nil
	}
	if uuid == "" || email == "" {
		return errors.New("default client UUID and email must be configured together")
	}
	c := Config{DefaultClientUUID: uuid, DefaultClientEmail: email}.DefaultClient()
	if err := validateClient(c); err != nil {
		return fmt.Errorf("invalid default client: %w", err)
	}
	return nil
}

func SaveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, b, true)
}

func InitConfig(path, listen, stateFile, xray string) (Config, error) {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("config already exists or cannot be inspected")
	}
	dir := filepath.Dir(path)
	c := Config{Listen: listen, BasePath: "/", Token: RandomHex(32), CertFile: filepath.Join(dir, "api.crt"), KeyFile: filepath.Join(dir, "api.key"), StateFile: stateFile, XrayBinary: xray, APIPort: 62789}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return c, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return c, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "3x-ui-node"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return c, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return c, err
	}
	if err = AtomicWrite(c.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), false); err != nil {
		return c, err
	}
	if err = AtomicWrite(c.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), false); err != nil {
		return c, err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	err = AtomicWrite(path, b, false)
	return c, err
}

func Fingerprint(c Config) (string, error) {
	b, err := os.ReadFile(c.CertFile)
	if err != nil {
		return "", err
	}
	p, _ := pem.Decode(b)
	if p == nil {
		return "", errors.New("invalid certificate")
	}
	sum := sha256.Sum256(p.Bytes)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}
