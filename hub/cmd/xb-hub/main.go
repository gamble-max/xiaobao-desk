package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"xiaobaodesk/hub/internal/config"
	"xiaobaodesk/hub/internal/hub"
	"xiaobaodesk/hub/internal/tlsutil"
)

func main() {
	var (
		configPath       = flag.String("config", "hub-config.json", "Path to hub JSON configuration file")
		dataDir          = flag.String("data-dir", "", "Directory for cert/key/fingerprint files (defaults to config directory)")
		listenAddr       = flag.String("listen", "", "Override listen address (e.g. :21116)")
		certPath         = flag.String("cert", "", "Override TLS certificate PEM path")
		keyPath          = flag.String("key", "", "Override TLS private key PEM path")
		initConfig       = flag.Bool("init-config", false, "Initialize config and TLS cert/fingerprint, then exit")
		printFingerprint = flag.Bool("print-fingerprint", false, "Ensure TLS cert exists, write hub-fingerprint.txt, print SHA-256 fingerprint, and exit")
	)
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = filepath.Dir(*configPath)
	}

	cfg, created, err := config.LoadOrInit(*configPath, dir)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}
	if *certPath != "" {
		cfg.CertPath = *certPath
	}
	if *keyPath != "" {
		cfg.KeyPath = *keyPath
	}

	cert, fp, err := tlsutil.EnsureCert(cfg.CertPath, cfg.KeyPath, cfg.FingerprintPath)
	if err != nil {
		log.Fatalf("tls cert error: %v", err)
	}

	if *printFingerprint {
		fmt.Println(fp)
		return
	}

	if *initConfig {
		// Never print auth_secret to stdout.
		fmt.Printf("config=%s (created=%v)\ncert=%s\nfingerprint_file=%s\ncert_sha256=%s\n",
			*configPath, created, cfg.CertPath, cfg.FingerprintPath, fp)
		return
	}

	logger := log.New(os.Stdout, "[xb-hub] ", log.LstdFlags)
	logger.Printf("starting xb-hub on %s (cert_sha256=%s)", cfg.ListenAddr, fp)

	tcpLn, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("tcp listen %s: %v", cfg.ListenAddr, err)
	}
	udpPC, err := net.ListenPacket("udp", cfg.ListenAddr)
	if err != nil {
		_ = tcpLn.Close()
		log.Fatalf("udp listen %s: %v", cfg.ListenAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := hub.NewServer(cfg, tlsutil.ServerTLSConfig(cert), logger)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := srv.ServeTCP(ctx, tcpLn); err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Printf("tcp server error: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := srv.ServeUDP(ctx, udpPC); err != nil && !errors.Is(err, net.ErrClosed) {
			logger.Printf("udp server error: %v", err)
		}
	}()

	<-ctx.Done()
	logger.Printf("shutting down...")
	_ = tcpLn.Close()
	_ = udpPC.Close()
	wg.Wait()
}
