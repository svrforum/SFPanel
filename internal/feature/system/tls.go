package system

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

// TLSStatus is what the settings screen renders.
type TLSStatus struct {
	Enabled bool `json:"enabled"`
	// Managed is false when the operator supplied their own certificate, in
	// which case the panel neither renews it nor offers a CA to download.
	Managed        bool      `json:"managed"`
	CANotAfter     time.Time `json:"ca_not_after,omitempty"`
	CAFingerprint  string    `json:"ca_fingerprint,omitempty"`
	CASubject      string    `json:"ca_subject,omitempty"`
	NotAfter       time.Time `json:"not_after,omitempty"`
	DNSNames       []string  `json:"dns_names,omitempty"`
	IPAddresses    []string  `json:"ip_addresses,omitempty"`
	DaysUntilRenew int       `json:"days_until_renew,omitempty"`
}

// GetTLSStatus reports the panel's certificate state.
//
// Local-only, so under ?node= the cluster proxy answers for the target node —
// which is what you want: each node runs its own authority, and an operator
// installing trust on a device needs that node's CA, not the one they happen to
// be pointed at.
func (h *Handler) GetTLSStatus(c echo.Context) error {
	if !h.TLSEnabled {
		return response.OK(c, TLSStatus{Enabled: false, Managed: false})
	}
	if !h.TLSManaged {
		return response.OK(c, TLSStatus{Enabled: true, Managed: false})
	}

	st, err := paneltls.New(h.TLSDir).Status()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTLSUnavailable,
			response.SanitizeOutput(err.Error()))
	}
	renewAt := st.NotAfter.Add(-paneltls.RenewWindow)
	return response.OK(c, TLSStatus{
		Enabled:        true,
		Managed:        true,
		CANotAfter:     st.CANotAfter,
		CAFingerprint:  st.CAFingerprint,
		CASubject:      st.CASubject,
		NotAfter:       st.NotAfter,
		DNSNames:       st.DNSNames,
		IPAddresses:    st.IPAddresses,
		DaysUntilRenew: int(time.Until(renewAt).Hours() / 24),
	})
}

// DownloadCACert serves the local CA certificate so an operator can install it
// on their devices and stop seeing certificate warnings.
//
// Certificate only. Neither the CA private key nor the server key is reachable
// from any route — paneltls.CACertPEM reads exactly one file, and there is no
// handler anywhere that reads the others.
func (h *Handler) DownloadCACert(c echo.Context) error {
	if !h.TLSEnabled || !h.TLSManaged {
		return response.Fail(c, http.StatusNotFound, response.ErrTLSDisabled,
			"this panel does not manage its own certificate authority")
	}
	mgr := paneltls.New(h.TLSDir)
	pem, err := mgr.CACertPEM()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTLSUnavailable,
			response.SanitizeOutput(err.Error()))
	}
	name := "sfpanel-ca.crt"
	if host, hostErr := os.Hostname(); hostErr == nil && host != "" {
		// Name the file after the node. An operator collecting one CA per node
		// otherwise ends up with sfpanel-ca(1).crt and no idea which is which.
		name = fmt.Sprintf("sfpanel-ca-%s.crt", host)
	}
	c.Response().Header().Set("Content-Type", "application/x-x509-ca-cert")
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))
	return c.Blob(http.StatusOK, "application/x-x509-ca-cert", pem)
}
