package node

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	masterResponseLimit = 4 * MaxBody
	masterTokenLimit    = 4096
)

// ValidateSHA256Fingerprint accepts the base64 format printed by the panel
// and a plain 64-character hex digest for easier non-interactive setup.
func ValidateSHA256Fingerprint(value string) error {
	if _, err := sha256Fingerprint(value); err != nil {
		return err
	}
	return nil
}

func sha256Fingerprint(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sha256/")
	if b, err := base64.StdEncoding.DecodeString(value); err == nil && len(b) == sha256.Size {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(b) == sha256.Size {
		return b, nil
	}
	if b, err := hex.DecodeString(value); err == nil && len(b) == sha256.Size {
		return b, nil
	}
	return nil, errors.New("must be a SHA-256 fingerprint in base64 or hex")
}

func readMasterToken(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("master sync token path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("master sync token file must be a regular 0600-or-stricter file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) > masterTokenLimit {
		return "", errors.New("master sync token file is too large")
	}
	token := strings.TrimSpace(string(b))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("master sync token file is empty or malformed")
	}
	return token, nil
}

func masterEndpoint(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("master sync baseURL must be an https URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/panel/api/inbounds/list"
	return u.String(), nil
}

type masterInboundEnvelope struct {
	Success bool      `json:"success"`
	Msg     string    `json:"msg"`
	Obj     []Inbound `json:"obj"`
}

// FetchMasterInbounds performs the only remote read used by synchronization.
// It intentionally has no method or path parameter so later callers cannot
// accidentally turn the sync credential into a general remote API client.
func FetchMasterInbounds(ctx context.Context, cfg MasterSyncConfig) ([]Inbound, error) {
	if !cfg.Enabled {
		return nil, errors.New("master sync is disabled")
	}
	if err := ValidateMasterSync(cfg); err != nil {
		return nil, err
	}
	token, err := readMasterToken(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	pin, err := sha256Fingerprint(cfg.CertSHA256)
	if err != nil {
		return nil, err
	}
	endpoint, err := masterEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         parsedEndpoint.Hostname(),
			RootCAs:            roots,
			InsecureSkipVerify: true, // VerifyConnection performs pin + chain + hostname checks.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("master response has no peer certificate")
				}
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
					return errors.New("master certificate fingerprint mismatch")
				}
				// The pinned leaf is an explicit trust anchor. This supports both
				// public CA certificates and a deliberately pinned self-signed
				// certificate while still enforcing expiry and hostname checks.
				pinnedRoots := x509.NewCertPool()
				for _, cert := range state.PeerCertificates[1:] {
					pinnedRoots.AddCert(cert)
				}
				pinnedRoots.AddCert(state.PeerCertificates[0])
				if _, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
					Roots:         pinnedRoots,
					Intermediates: roots,
					DNSName:       state.ServerName,
					KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				}); err != nil {
					return fmt.Errorf("master certificate verification failed: %w", err)
				}
				return nil
			},
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("master API redirect refused")
		},
	}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("master API returned %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, masterResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > masterResponseLimit {
		return nil, errors.New("master API response exceeds 4 MiB")
	}
	var envelope masterInboundEnvelope
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, fmt.Errorf("invalid master API JSON: %w", err)
	}
	if !envelope.Success {
		if envelope.Msg == "" {
			envelope.Msg = "unknown master API error"
		}
		return nil, fmt.Errorf("master API: %s", envelope.Msg)
	}
	if envelope.Obj == nil {
		return nil, errors.New("master API returned null inbound list")
	}
	return envelope.Obj, nil
}
