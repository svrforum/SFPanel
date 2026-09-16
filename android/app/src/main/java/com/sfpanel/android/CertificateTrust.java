package com.sfpanel.android;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.http.SslCertificate;
import android.net.http.SslError;
import android.os.Bundle;
import java.io.ByteArrayInputStream;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;

final class CertificateTrust {
    private final SharedPreferences pins;
    CertificateTrust(Context context) { pins = context.getSharedPreferences("server_certificates", Context.MODE_PRIVATE); }
    String pin(String origin) { return pins.getString(ServerAddress.normalize(origin), null); }
    void save(String origin, String pin) { pins.edit().putString(ServerAddress.normalize(origin), pin).apply(); }
    void remove(String origin) { pins.edit().remove(ServerAddress.normalize(origin)).apply(); }
    boolean accepts(String origin, SslError error) {
        String target = error.getUrl();
        if (target != null && target.startsWith("wss://")) target = "https://" + target.substring(6);
        if (!ServerAddress.sameOrigin(origin, target)) return false;
        if (error.hasError(SslError.SSL_EXPIRED) || error.hasError(SslError.SSL_NOTYETVALID) || error.hasError(SslError.SSL_INVALID)) return false;
        try {
            Bundle state = SslCertificate.saveState(error.getCertificate());
            byte[] der = state == null ? null : state.getByteArray("x509-certificate");
            if (der == null) return false;
            X509Certificate cert = (X509Certificate) CertificateFactory.getInstance("X.509").generateCertificate(new ByteArrayInputStream(der));
            PrivateTls.pinned(pin(origin)).checkServerTrusted(new X509Certificate[]{cert}, cert.getPublicKey().getAlgorithm());
            return true;
        } catch (Exception e) { return false; }
    }
}
