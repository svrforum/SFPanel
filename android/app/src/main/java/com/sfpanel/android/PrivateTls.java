package com.sfpanel.android;

import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URL;
import java.security.MessageDigest;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import javax.net.ssl.*;

/** Per-connection certificate trust. Never changes process-wide TLS defaults. */
final class PrivateTls {
    static String fingerprint(X509Certificate cert) throws Exception {
        StringBuilder out = new StringBuilder();
        for (byte b : MessageDigest.getInstance("SHA-256").digest(cert.getEncoded())) out.append(String.format(java.util.Locale.ROOT, "%02X", b & 255));
        return out.toString();
    }

    static X509TrustManager pinned(String pin) {
        return new X509TrustManager() {
            public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
            public void checkClientTrusted(X509Certificate[] chain, String type) throws CertificateException { throw new CertificateException("Client certificates unsupported"); }
            public void checkServerTrusted(X509Certificate[] chain, String type) throws CertificateException {
                try {
                    if (chain == null || chain.length == 0 || pin == null || !pin.equals(fingerprint(chain[0]))) throw new CertificateException("Certificate changed");
                    chain[0].checkValidity();
                } catch (CertificateException e) { throw e; }
                catch (Exception e) { throw new CertificateException(e); }
            }
        };
    }

    static SSLSocketFactory factory(X509TrustManager trust) throws Exception {
        SSLContext context = SSLContext.getInstance("TLS");
        context.init(null, new TrustManager[]{trust}, null);
        return context.getSocketFactory();
    }

    static void configure(java.net.HttpURLConnection connection, String pin) throws Exception {
        if (pin == null || !(connection instanceof HttpsURLConnection)) return;
        HttpsURLConnection https = (HttpsURLConnection) connection;
        https.setSSLSocketFactory(factory(pinned(pin)));
        // The user confirms the exact certificate for this exact origin, including
        // private hosts/IPs not listed in its SAN. Still require the leaf pin here.
        https.setHostnameVerifier((hostname, session) -> {
            try { return pin.equals(fingerprint((X509Certificate) session.getPeerCertificates()[0])); }
            catch (Exception e) { return false; }
        });
    }

    static X509Certificate inspect(String origin) throws Exception {
        URL url = new URL(origin);
        if (!url.getProtocol().equals("https")) throw new CertificateException("HTTPS required");
        X509Certificate[] found = new X509Certificate[1];
        X509TrustManager capture = new X509TrustManager() {
            public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
            public void checkClientTrusted(X509Certificate[] c, String t) throws CertificateException { throw new CertificateException(); }
            public void checkServerTrusted(X509Certificate[] c, String t) throws CertificateException {
                if (c != null && c.length > 0) found[0] = c[0];
                // Inspection intentionally aborts TLS: no HTTP or credentials sent.
                throw new CertificateException("Inspection only");
            }
        };
        int port = url.getPort() == -1 ? 443 : url.getPort();
        try (Socket tcp = new Socket()) {
            tcp.connect(new InetSocketAddress(url.getHost(), port), 8000); tcp.setSoTimeout(8000);
            try (SSLSocket tls = (SSLSocket) factory(capture).createSocket(tcp, url.getHost(), port, true)) {
                tls.setSoTimeout(8000);
                try { tls.startHandshake(); } catch (SSLException expected) { if (found[0] == null) throw expected; }
            }
        }
        if (found[0] == null) throw new CertificateException("No certificate");
        found[0].checkValidity();
        return found[0];
    }
}
