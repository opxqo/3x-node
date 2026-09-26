package main

import (
	stdbytes "bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/opxqo/3x-node/v3/internal/node"
)

const accessBlobPrefix = "3xn1."

var enrollCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// accessInfo is shared by the enroll request and the credentials blob so the
// master parses one shape; Code and URL are each set by only one of them.
type accessInfo struct {
	Code        string `json:"code,omitempty"`
	URL         string `json:"url,omitempty"`
	Token       string `json:"token"`
	CertSHA256  string `json:"certSha256"`
	ListenPort  string `json:"listenPort"`
	BasePath    string `json:"basePath"`
	Version     string `json:"version"`
	XrayVersion string `json:"xrayVersion"`
	GUID        string `json:"guid"`
}

func loadAccessInfo(c node.Config) (accessInfo, error) {
	pin, err := node.Fingerprint(c)
	if err != nil {
		return accessInfo{}, err
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return accessInfo{}, err
	}
	state, err := node.LoadState(c.StateFile)
	if err != nil {
		return accessInfo{}, err
	}
	return accessInfo{Token: c.Token, CertSHA256: pin, ListenPort: port, BasePath: c.BasePath, Version: node.Version, XrayVersion: node.XrayVersion, GUID: state.GUID}, nil
}

func requireHTTPSURL(raw, name string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("%s must be an https URL without credentials or fragment", name)
	}
	return nil
}

func accessBlob(info accessInfo, publicURL string) (string, error) {
	if publicURL != "" {
		if err := requireHTTPSURL(publicURL, "-public-url"); err != nil {
			return "", err
		}
		info.URL = publicURL
	}
	b, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	return accessBlobPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func enrollClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// enroll posts this node's access details to the master once; the node keeps
// no master URL or credential, so a failed call is retried by hand.
func enroll(client *http.Client, endpoint, code string, info accessInfo, out io.Writer) error {
	if err := requireHTTPSURL(endpoint, "-url"); err != nil {
		return err
	}
	if !enrollCodePattern.MatchString(code) {
		return errors.New("-code must be 16-128 characters of A-Z, a-z, 0-9, _ or -")
	}
	info.Code = code
	body, err := json.Marshal(info)
	if err != nil {
		return err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Post(endpoint, "application/json", stdbytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("enroll request failed: %w", err)
	}
	defer resp.Body.Close()
	var reply node.Envelope
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, node.MaxBody)).Decode(&reply)
	if resp.StatusCode < 200 || resp.StatusCode > 299 || decodeErr != nil || !reply.Success {
		msg := reply.Msg
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("enroll rejected: %s", msg)
	}
	fmt.Fprintln(out, "Enrolled with the master.", reply.Msg)
	return nil
}
