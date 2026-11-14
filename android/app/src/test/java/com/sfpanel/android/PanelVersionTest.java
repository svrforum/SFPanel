package com.sfpanel.android;

import org.junit.Test;
import static org.junit.Assert.*;

public class PanelVersionTest {
    @Test public void thePanelsOwnInputAreaStartsWithTheVersionThatShipsIt() {
        assertTrue(PanelVersion.webInput("0.86.0"));
        assertTrue(PanelVersion.webInput("0.86.1"));
        assertTrue(PanelVersion.webInput("0.100.0"));
        assertTrue(PanelVersion.webInput("1.0.0"));
        // As a tag is written, and as a build ahead of its tag reports itself.
        assertTrue(PanelVersion.webInput("v0.86.0"));
        assertTrue(PanelVersion.webInput("0.86.0-3-gabc1234"));
    }
    @Test public void anOlderPanelKeepsTheAppsOwnKeys() {
        // Its page hides nothing and draws no composer: the app's keys are the only ones.
        assertFalse(PanelVersion.webInput("0.85.0"));
        assertFalse(PanelVersion.webInput("0.85.99"));
        assertFalse(PanelVersion.webInput("0.9.0"));
    }
    @Test public void aVersionThatCannotBeReadIsTreatedAsOlder() {
        // The app's keys work with every panel; the panel's own only from 0.86.0.
        for (String version : new String[]{null, "", "dev", "0.86", "0.86.x", "86", "0.86.0.1", "latest", "0.-1.0", "999999.0.0"})
            assertFalse(String.valueOf(version), PanelVersion.webInput(version));
    }
}
