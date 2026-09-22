// devgateway is a development-only credential bridge for the web frontend.
//
// Production sign-in belongs to a real gateway that owns the signing keys;
// this binary exists so the browser can exercise the same topology locally:
// the frontend holds short-lived dual JWTs obtained from /devgateway/login,
// attaches them to every request, and devgateway proxies them to cloud
// verbatim. The private key never reaches frontend code. Never deploy.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer     = "ora-internal-issuer"
	audience   = "ora-cloud"
	kidService = "local-gateway-verify" // must match auth.keys ids in configs/config.yaml
	kidUser    = "local-user-verify"
	serviceSub = "devgateway-instance" // user caller must equal service sub
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address")
	cloudURL := flag.String("cloud", "http://127.0.0.1:8080", "upstream cloud base URL")
	keyFile := flag.String("key", filepath.Join(".local", "minttoken", "keys", "private.pem"), "dev Ed25519 private key PEM (auto-generated when missing)")
	flag.Parse()

	key, err := loadOrCreateKey(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	target, err := url.Parse(*cloudURL)
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	http.HandleFunc("/devgateway/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": issuer, "aud": []string{audience}, "sub": q.Get("subject"),
			"kind": "user", "source": q.Get("source"), "displayName": q.Get("display"),
			"caller": serviceSub, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		}
		service, user := mint(key, claims)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"serviceToken": service, "userToken": user, "expiresAt": now.Add(5 * time.Minute).Format(time.RFC3339)}); err != nil {
			log.Printf("login response: %v", err)
		}
	})
	http.Handle("/", proxy)
	log.Printf("devgateway listening on http://%s proxying %s (key %s)", *addr, *cloudURL, *keyFile)
	// A plain ListenAndServe has no read header timeout; use an explicit Server.
	// Read and write timeouts stay zero: the proxy forwards long-lived SSE
	// streams, and a write deadline would sever them. Development tool only.
	server := &http.Server{Addr: *addr, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// mint signs the dual credentials: a gateway service token and a final-user
// token whose caller binds to the same service subject.
func mint(key ed25519.PrivateKey, user jwt.MapClaims) (service, userToken string) {
	now := time.Now()
	sign := func(kid string, claims jwt.MapClaims) string {
		t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		t.Header["kid"] = kid
		s, err := t.SignedString(key)
		if err != nil {
			panic(err)
		}
		return s
	}
	service = sign(kidService, jwt.MapClaims{
		"iss": issuer, "aud": []string{audience}, "sub": serviceSub,
		"kind": "service", "role": "gateway",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	})
	return service, sign(kidUser, user)
}

// loadOrCreateKey reuses the manual-verification key pair so one
// configs/config.yaml works for both flows; a missing key is generated and the
// corresponding public PEM is written for the cloud trust configuration.
func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("decode %s: invalid private pem", path)
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s: ed25519 private key required", path)
		}
		return key, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil {
		return nil, err
	}
	public := filepath.Join(filepath.Dir(path), "public.pem")
	if err := os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600); err != nil {
		return nil, err
	}
	fmt.Printf("generated dev keys: %s and %s; configure auth.keys public_key_file accordingly\n", path, public)
	return priv, nil
}
