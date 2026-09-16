package com.sfpanel.android;

import javax.crypto.Cipher;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

final class SessionCipher {
    static String encrypt(SecretKey key, String origin, String value) throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.ENCRYPT_MODE, key);
        cipher.updateAAD(origin.getBytes(StandardCharsets.UTF_8));
        byte[] encrypted = cipher.doFinal(value.getBytes(StandardCharsets.UTF_8));
        return Base64.getEncoder().encodeToString(cipher.getIV()) + ":" + Base64.getEncoder().encodeToString(encrypted);
    }
    static String decrypt(SecretKey key, String origin, String value) throws Exception {
        String[] parts = value.split(":", 2);
        if (parts.length != 2) throw new java.security.GeneralSecurityException("Invalid session record");
        byte[] iv = Base64.getDecoder().decode(parts[0]);
        if (iv.length != 12) throw new java.security.GeneralSecurityException("Invalid IV");
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, key, new GCMParameterSpec(128, iv));
        cipher.updateAAD(origin.getBytes(StandardCharsets.UTF_8));
        return new String(cipher.doFinal(Base64.getDecoder().decode(parts[1])), StandardCharsets.UTF_8);
    }
}
