package com.sfpanel.android;

import org.junit.Test;
import static org.junit.Assert.*;

public class ServerAddressTest {
    @Test public void canonicalOriginsKeepPortsAndIpv6() {
        assertEquals("https://panel.example.com:3628", ServerAddress.normalize(" panel.example.com:3628/ "));
        assertEquals("http://192.168.1.10:3628", ServerAddress.normalize("http://192.168.1.10:3628"));
        assertEquals("https://panel.example.com", ServerAddress.normalize("HTTPS://PANEL.EXAMPLE.COM:443///"));
        assertEquals("http://[::1]:3628", ServerAddress.normalize("http://[::1]:3628/"));
    }

    @Test public void rejectsCredentialsPathsAndNonWebSchemes() {
        for (String input : new String[] { "", "javascript:alert(1)", "file:///etc/passwd",
                "https://user:password@panel.test", "https://panel.test/dashboard", "https://panel.test/?token=x",
                "https://panel.test/#login", "https://panel.test:0", "https://panel.test:65536",
                "https://panel.test:", "https://panel.test\\@evil.test", "https://panel.test/%2f",
                "https://panel.test\n.evil.test", "intent://panel.test", "https://" }) {
            assertThrows(input, IllegalArgumentException.class, () -> ServerAddress.normalize(input));
        }
    }

    @Test public void navigationAndCredentialsAreBoundToExactOrigin() {
        String origin = "https://panel.test:3628";
        assertTrue(ServerAddress.sameOrigin(origin, "https://panel.test:3628/files?path=%2F"));
        assertFalse(ServerAddress.sameOrigin(origin, "http://panel.test:3628/files"));
        assertFalse(ServerAddress.sameOrigin(origin, "https://panel.test/files"));
        assertFalse(ServerAddress.sameOrigin(origin, "https://panel.test.evil.test:3628"));
        assertFalse(ServerAddress.sameOrigin(origin, "https://panel.test:3628@evil.test"));
        assertFalse(ServerAddress.sameOrigin(origin, "https://user@panel.test:3628"));
        assertFalse(ServerAddress.sameOrigin(origin, "blob:https://panel.test:3628/id"));
        assertFalse(ServerAddress.isWebLink("intent://host/#Intent;end"));
        assertFalse(ServerAddress.isWebLink("file:///sdcard/secrets"));
    }
}
