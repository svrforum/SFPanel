package com.sfpanel.android;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import java.security.KeyStore;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;

/** Tokens are encrypted per server origin; the key never leaves Android Keystore. */
final class SessionVault {
    private static final String ALIAS = "sfpanel.login.v1";
    private final SharedPreferences storage;
    SessionVault(Context context) { storage = context.getSharedPreferences("encrypted_sessions", Context.MODE_PRIVATE); }
    private SecretKey key() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore"); store.load(null);
        if (store.containsAlias(ALIAS)) return ((KeyStore.SecretKeyEntry) store.getEntry(ALIAS, null)).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).setKeySize(256).build());
        return generator.generateKey();
    }
    String read(String origin) {
        String encrypted = storage.getString(origin, null);
        if (encrypted == null) return null;
        try { return SessionCipher.decrypt(key(), origin, encrypted); }
        catch (Exception e) { remove(origin); return null; }
    }
    boolean write(String origin, String json) {
        try { return storage.edit().putString(origin, SessionCipher.encrypt(key(), origin, json)).commit(); }
        catch (Exception e) { return false; }
    }
    void remove(String origin) { storage.edit().remove(origin).commit(); }
    void clear() { storage.edit().clear().commit(); }
}
