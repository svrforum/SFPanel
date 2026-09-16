package com.sfpanel.android;

import org.junit.Test;
import static org.junit.Assert.*;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;

public class SessionCipherTest {
    private SecretKey key() throws Exception { KeyGenerator generator = KeyGenerator.getInstance("AES"); generator.init(256); return generator.generateKey(); }
    @Test public void encryptedRoundTripAndRandomIv() throws Exception {
        SecretKey key = key(); String plain = "{\"token\":\"test-access\",\"refresh_token\":\"test-refresh\"}";
        String first = SessionCipher.encrypt(key, "https://a.test", plain);
        assertFalse(first.contains("test-access"));
        assertEquals(plain, SessionCipher.decrypt(key, "https://a.test", first));
        assertNotEquals(first, SessionCipher.encrypt(key, "https://a.test", plain));
    }
    @Test public void otherOriginsKeysAndModifiedCiphertextAreRejected() throws Exception {
        SecretKey key = key(); String value = SessionCipher.encrypt(key, "https://a.test", "credentials");
        assertThrows(Exception.class, () -> SessionCipher.decrypt(key, "https://a.test:8443", value));
        assertThrows(Exception.class, () -> SessionCipher.decrypt(key(), "https://a.test", value));
        String[] parts = value.split(":"); byte[] bytes = java.util.Base64.getDecoder().decode(parts[1]); bytes[0] ^= 1;
        String modified = parts[0] + ":" + java.util.Base64.getEncoder().encodeToString(bytes);
        assertThrows(Exception.class, () -> SessionCipher.decrypt(key, "https://a.test", modified));
    }
}
