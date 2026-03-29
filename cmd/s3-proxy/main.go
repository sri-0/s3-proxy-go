package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/sri/s3-proxy-go/internal/backend"
	"github.com/sri/s3-proxy-go/internal/config"
	"github.com/sri/s3-proxy-go/internal/proxy"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_FILE")
	}
	if *configPath == "" {
		*configPath = "config.yaml"
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	backends := make(map[string]backend.Backend)
	for _, acc := range cfg.Accounts {
		accessKey := os.Getenv(acc.AccessKeyEnvVar)
		secretKey := os.Getenv(acc.SecretKeyEnvVar)
		if accessKey == "" {
			log.Fatalf("account %s: env var %s is not set", acc.Name, acc.AccessKeyEnvVar)
		}
		if secretKey == "" {
			log.Fatalf("account %s: env var %s is not set", acc.Name, acc.SecretKeyEnvVar)
		}

		be, err := backend.NewMinioBackend(acc.Endpoint, accessKey, secretKey, acc.UseSSL)
		if err != nil {
			log.Fatalf("account %s: failed to create backend: %v", acc.Name, err)
		}
		backends[acc.Name] = be
		log.Printf("connected to backend %s at %s", acc.Name, acc.Endpoint)
	}

	router := proxy.BuildRouter(cfg, backends)

	log.Printf("s3-proxy listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, router))
}
