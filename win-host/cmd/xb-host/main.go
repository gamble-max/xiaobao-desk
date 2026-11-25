package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/xiaobaodesk/win-host/internal/config"
	"github.com/xiaobaodesk/win-host/internal/displayguard"
	"github.com/xiaobaodesk/win-host/internal/hubclient"
	"github.com/xiaobaodesk/win-host/internal/ipc"
	"github.com/xiaobaodesk/win-host/internal/p2p"
	"github.com/xiaobaodesk/win-host/internal/protocol"
	"github.com/xiaobaodesk/win-host/internal/security"
	"github.com/xiaobaodesk/win-host/internal/session"
)

func main() {
	cfgPath := flag.String("config", config.DefaultConfigPath(), "Path to host JSON config")
	initCfg := flag.Bool("init-config", false, "Generate restricted local config with random strong password & TOTP secret (never printed)")
	force := flag.Bool("force", false, "Overwrite existing config on --init-config")
	printFP := flag.Bool("print-fingerprint", false, "Print host TLS certificate SHA-256 fingerprint")
	hubOverride := flag.String("hub", "", "Override hub address (host:port)")
	listenOverride := flag.String("listen", "", "Override P2P listen address")
	noHub := flag.Bool("no-hub", false, "Disable outbound xb-hub signaling")
	noMedia := flag.Bool("no-media", false, "Disable launching xb-media-core.exe")
	logPath := flag.String("log", "", "Optional path to append log output")
	flag.Parse()

	if *logPath != "" {
		if err := os.MkdirAll(filepath.Dir(*logPath), 0o700); err == nil {
			if lf, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				defer lf.Close()
				log.SetOutput(lf)
			}
		}
	}

	if *initCfg {
		cfg, fp, err := config.InitConfig(*cfgPath, *force)
		if err != nil {
			log.Fatalf("init-config failed: %v", err)
		}
		// Never print HostPassword or TOTPSecret!
		fmt.Printf("Initialized restricted config: %s\n", *cfgPath)
		fmt.Printf("Host ID: %s\n", cfg.HostID)
		fmt.Printf("Host TLS SHA-256 Fingerprint: %s\n", fp)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config (%s): %v (run with --init-config first)", *cfgPath, err)
	}
	displayguard.EnsureNoVirtualDisplayAttached()
	if *hubOverride != "" {
		cfg.HubAddr = *hubOverride
	}
	if *listenOverride != "" {
		cfg.ListenAddr = *listenOverride
	}

	cert, fp, err := security.EnsureSelfSignedCert(cfg.CertPath, cfg.KeyPath, "xb-host")
	if err != nil {
		log.Fatalf("tls cert error: %v", err)
	}
	_ = config.RestrictFilePermissions(cfg.KeyPath)
	if *printFP {
		fmt.Println(fp)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var sup *ipc.Supervisor
	if !*noMedia && cfg.MediaCorePath != "" {
		sup = ipc.NewSupervisor(cfg.MediaCorePath)
		go sup.Run(ctx)
	}

	if !*noHub && cfg.HubAddr != "" {
		hc := hubclient.New(cfg, fp, func(conn net.Conn, fc *protocol.FrameConn, bind protocol.RelayBindMsg) {
			session.HandleConnection(conn, fc, cfg, sup, "relay:"+bind.SessionID)
		})
		go hc.Run(ctx)
	}

	tlsCfg := security.NewServerTLSConfig(cert)
	ln := p2p.NewListener(cfg.ListenAddr, tlsCfg, func(conn net.Conn, fc *protocol.FrameConn) {
		session.HandleConnection(conn, fc, cfg, sup, "p2p:"+conn.RemoteAddr().String())
	})
	hostName, _ := os.Hostname()
	if hostName == "" {
		hostName = "Windows PC"
	}
	listenPort := 21118
	if _, pStr, err := net.SplitHostPort(cfg.ListenAddr); err == nil {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			listenPort = p
		}
	}
	ln.SetAnnounceInfo(cfg.HostID, hostName, listenPort, fp)

	log.Printf("[xb-host] starting (host_id=%s, listen=%s, cert_sha256=%s)", cfg.HostID, cfg.ListenAddr, fp)
	if err := ln.Serve(ctx); err != nil {
		log.Fatalf("p2p listener failed: %v", err)
	}
}
