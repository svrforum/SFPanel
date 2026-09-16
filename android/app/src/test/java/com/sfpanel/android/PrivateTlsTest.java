package com.sfpanel.android;

import org.junit.Test;
import static org.junit.Assert.*;
import java.security.cert.*;

public class PrivateTlsTest {
    private X509Certificate cert(String name) throws Exception {
        try (java.io.InputStream in = getClass().getResourceAsStream("/" + name + ".pem")) {
            return (X509Certificate) CertificateFactory.getInstance("X.509").generateCertificate(in);
        }
    }
    @Test public void onlyApprovedLeafIsTrusted() throws Exception {
        X509Certificate approved = cert("private");
        javax.net.ssl.X509TrustManager trust = PrivateTls.pinned(PrivateTls.fingerprint(approved));
        trust.checkServerTrusted(new X509Certificate[]{approved}, "RSA");
        assertThrows(CertificateException.class, () -> trust.checkServerTrusted(new X509Certificate[]{cert("changed")}, "RSA"));
        assertThrows(CertificateException.class, () -> trust.checkServerTrusted(new X509Certificate[0], "RSA"));
        assertThrows(CertificateException.class, () -> PrivateTls.pinned(null).checkServerTrusted(new X509Certificate[]{approved}, "RSA"));
    }
    @Test public void expiredApprovedCertificateIsRejected() throws Exception {
        X509Certificate expired = cert("expired");
        assertThrows(CertificateExpiredException.class, () -> PrivateTls.pinned(PrivateTls.fingerprint(expired)).checkServerTrusted(new X509Certificate[]{expired}, "RSA"));
    }
    @Test public void hostOrPortDoesNotShareTrust() {
        assertFalse(ServerAddress.sameOrigin("https://private.test", "https://private.test:8443/api"));
        assertFalse(ServerAddress.sameOrigin("https://private.test", "https://other.test/api"));
        assertFalse(ServerAddress.sameOrigin("https://private.test", "http://private.test/api"));
    }
}
