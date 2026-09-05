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
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen     string `json:"listen"`
	BasePath   string `json:"basePath"`
	Token      string `json:"token"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
	StateFile  string `json:"stateFile"`
	XrayBinary string `json:"xrayBinary"`
	APIPort    int    `json:"xrayApiPort"`
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
	return c, nil
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
