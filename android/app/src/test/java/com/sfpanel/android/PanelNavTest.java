package com.sfpanel.android;

import java.util.List;
import org.junit.Test;
import static org.junit.Assert.*;

public class PanelNavTest {
    static final String BRIDGE = "{\"v\":1,\"server\":{\"name\":\"agent-1\",\"role\":\"agent\",\"attached\":false},\"nav\":[" +
            "{\"group\":\"server\",\"title\":\"agent-1\",\"items\":[{\"label\":\"System\",\"path\":\"/services\",\"tabs\":[{\"label\":\"Services\",\"path\":\"/services\"},{\"label\":\"Processes\",\"path\":\"/processes\"}]}," +
            "{\"label\":\"Evil\",\"path\":\"//evil.example\",\"tabs\":[]}]}," +
            "{\"group\":\"tail\",\"title\":\"\",\"items\":[{\"label\":\"Settings\",\"path\":\"/settings\",\"tabs\":[]}]}]}";

    @Test public void readsAVersionOneBridge() {
        PanelNav n = PanelNav.parse(BRIDGE);
        assertEquals("agent-1", n.serverName);
        assertEquals(2, n.groups.size());
        assertEquals(1, n.groups.get(0).items.size());   // Evil is dropped
    }
    @Test public void anythingButVersionOneFallsBackToTheBuiltInList() {
        for (String s : new String[]{null, "null", "", "{", BRIDGE.replace("\"v\":1", "\"v\":2"), BRIDGE.replace("\"v\":1,", ""), "{\"v\":1,\"server\":null,\"nav\":[]}"})
            assertNull(String.valueOf(s), PanelNav.parse(s));
    }
    @Test public void onlySameOriginPathsAreFollowed() {
        assertTrue(PanelNav.safePath("/processes"));
        assertTrue(PanelNav.safePath("/settings?tab=alerts"));
        for (String p : new String[]{null, "", "processes", "//evil.example", "https://evil.example", "/\\evil.example", "/a\nb"})
            assertFalse(String.valueOf(p), PanelNav.safePath(p));
    }
    @Test public void theTitleFollowsTheServer() {
        assertEquals("agent-1", PanelNav.title("agent-1", "Home lab", false, "Terminal"));
        assertEquals("agent-1 · Terminal", PanelNav.title("agent-1", "Home lab", true, "Terminal"));
        assertEquals("Home lab", PanelNav.title(null, "Home lab", false, "Terminal"));
    }
    @Test public void searchReachesTabs() {
        List<List<PanelNav.Hit>> hits = PanelNav.parse(BRIDGE).search("proc");
        assertEquals("System › Processes", hits.get(0).get(0).label);
        assertEquals("/processes", hits.get(0).get(0).path);
        // TalkBack reads "›" out as a symbol: a tab is said as its entry, then itself.
        assertEquals("System, Processes", hits.get(0).get(0).description);
    }
    @Test public void theCurrentPlaceIncludesTabs() {
        PanelNav n = PanelNav.parse(BRIDGE);
        PanelNav.Item system = n.groups.get(0).items.get(0);
        assertTrue(n.itemSelected(system, "/processes"));
        assertFalse(n.itemSelected(system, "/processesx"));
    }
    @Test public void aServerWithoutANameIsNoServer() {
        assertNull(PanelNav.parse(BRIDGE.replace("\"name\":\"agent-1\"", "\"name\":\"\"")).serverName);
        assertNull(PanelNav.parse(BRIDGE.replace("{\"name\":\"agent-1\",\"role\":\"agent\",\"attached\":false}", "null")).serverName);
    }
    @Test public void anEmptySearchListsItemsAndNotTheirTabs() {
        List<List<PanelNav.Hit>> hits = PanelNav.parse(BRIDGE).search("");
        assertEquals(2, hits.size());
        assertEquals(1, hits.get(0).size());
        assertEquals("System", hits.get(0).get(0).label);
        assertFalse(hits.get(0).get(0).tab);
        assertNull(hits.get(0).get(0).description);   // an entry is said by its label
        assertEquals("Settings", hits.get(1).get(0).label);
    }
    static final String FLEET = "{\"v\":1,\"server\":null,\"nav\":[{\"group\":\"fleet\",\"title\":\"All servers\",\"items\":[" +
            "{\"label\":\"Running\",\"path\":\"/cluster/running\",\"tabs\":[]}," +
            "{\"label\":\"Cluster\",\"path\":\"/cluster\",\"tabs\":[{\"label\":\"Overview\",\"path\":\"/cluster/overview\"}]}]}]}";
    @Test public void thePageBelongsToTheLongestAddressThatHoldsIt() {
        PanelNav n = PanelNav.parse(FLEET);
        PanelNav.Item running = n.groups.get(0).items.get(0), cluster = n.groups.get(0).items.get(1);
        assertTrue(n.itemSelected(running, "/cluster/running"));
        assertFalse(n.itemSelected(cluster, "/cluster/running"));
        assertTrue(n.itemSelected(cluster, "/cluster/overview"));
        assertTrue(n.itemSelected(cluster, "/cluster"));
    }
}
