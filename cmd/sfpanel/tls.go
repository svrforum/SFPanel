package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

// selfPanel describes how in-process code reaches this node's own panel port.
// Everything that used to hardcode "http://127.0.0.1:<port>" goes through it,
// so the scheme decision exists once instead of in five places.
func selfPanel(cfg *config.Config) paneltls.Self {
	return paneltls.Self{
		TLSEnabled: cfg.Server.TLS.Enabled,
		Dir:        cfg.Server.TLS.Dir,
		CertFile:   cfg.Server.TLS.CertFile,
		CAFile:     cfg.Server.TLS.CAFile,
		Port:       cfg.Server.Port,
	}
}

// prepareTLS resolves the certificate pair the HTTP server should present, or
// ("", "", nil) when the panel is to serve plain HTTP.
//
// Two modes, and the difference between them is who owns the material:
//
//   - Managed (the default when tls.enabled is set): the panel keeps a local CA
//     and issues itself a certificate, renewing it when it nears expiry or when
//     the host's addresses change. Missing or damaged files are regenerated,
//     because a panel that will not start is worse than one that hands out a
//     fresh certificate signed by the CA every device already trusts.
//   - Operator-supplied (cert_file and key_file both set): the panel touches
//     nothing. If those files cannot be loaded it refuses to start rather than
//     quietly substituting a self-issued certificate — an operator who provided
//     a certificate did so deliberately, and silently serving a different one
//     would be a worse outcome than a loud failure.
func prepareTLS(cfg *config.Config) (certFile, keyFile string, err error) {
	if !cfg.Server.TLS.Enabled {
		return "", "", nil
	}

	if !cfg.Server.TLS.Managed() {
		for label, path := range map[string]string{
			"server.tls.cert_file": cfg.Server.TLS.CertFile,
			"server.tls.key_file":  cfg.Server.TLS.KeyFile,
		} {
			if _, statErr := os.Stat(path); statErr != nil {
				return "", "", fmt.Errorf("%s: %w", label, statErr)
			}
		}
		slog.Info("serving HTTPS with an operator-supplied certificate",
			"component", "tls", "cert", cfg.Server.TLS.CertFile)
		return cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile, nil
	}

	mgr := paneltls.New(cfg.Server.TLS.Dir)
	res, err := mgr.Ensure()
	if err != nil {
		return "", "", fmt.Errorf("prepare panel certificate: %w", err)
	}

	// A replaced CA is the one event with a real human cost: every device that
	// installed the old anchor has to install the new one, and until it does the
	// browser shows a warning again. It must never scroll past unnoticed.
	if res.CAAction == paneltls.ActionIssuedCA {
		slog.Warn("generated a new local certificate authority — every device that trusted the previous one must install the new ca.crt",
			"component", "tls",
			"path", mgr.CACertPath(),
			"expires", res.CANotAfter.Format("2006-01-02"))
	}
	if res.LeafAction == paneltls.ActionIssuedLeaf {
		slog.Info("issued panel certificate",
			"component", "tls",
			"reason", res.LeafReason,
			"expires", res.NotAfter.Format("2006-01-02"),
			"names", res.DNSNames,
			"addresses", res.IPs)
	}

	return mgr.ServerCertPath(), mgr.ServerKeyPath(), nil
}
