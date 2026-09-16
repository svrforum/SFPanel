package com.sfpanel.android;

import org.junit.Test;
import static org.junit.Assert.*;

public class UpdateVersionTest {
    @Test public void androidTagsMatchPublishingVersionCodes() {
        assertEquals(1001, UpdateVersion.code("android-v0.1.0"));
        assertEquals(1002, UpdateVersion.code("android-v0.1.1"));
        assertEquals(1000001, UpdateVersion.code("android-v1.0.0"));
        assertTrue(UpdateVersion.code("android-v0.2.0") > UpdateVersion.code("android-v0.1.999"));
    }
    @Test public void serverTagsAndInvalidVersionsNeverOfferUpdates() {
        for (String tag : new String[]{"v0.73.0", "android-v0.1.1-beta", "android-v01.1.1", "android-v1.1000.0", "android-v1.0.1000", "android-v9999999999999999999.0.0", null}) assertEquals(-1, UpdateVersion.code(tag));
    }
}
