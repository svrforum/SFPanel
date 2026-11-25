package com.sfpanel.android;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;
import org.json.JSONTokener;

/** The menu and server name a panel page publishes on window.__sfpanel, read
 * through evaluateJavascript. Anything the app does not recognise yields null,
 * and the app keeps its own list and the saved connection's name. */
final class PanelNav {
    static final class Link {
        final String label, path;
        Link(String label, String path) { this.label = label; this.path = path; }
    }
    static final class Item {
        final String label, path;
        final List<Link> tabs;
        Item(String label, String path, List<Link> tabs) { this.label = label; this.path = path; this.tabs = tabs; }
    }
    static final class Group {
        final String title;
        final List<Item> items;
        Group(String title, List<Item> items) { this.title = title; this.items = items; }
    }
    /** One button in the sheet. A tab reads "Item › Tab" and is said "Item, Tab",
     * as TalkBack reads "›" out as a symbol; item is the entry it belongs to. */
    static final class Hit {
        final String label, path;
        /** What a screen reader says instead of the label; null to say the label. */
        final String description;
        final boolean tab;
        final Item item;
        Hit(String label, String description, String path, boolean tab, Item item) { this.label = label; this.description = description; this.path = path; this.tab = tab; this.item = item; }
    }

    final String serverName;
    final List<Group> groups;

    private PanelNav(String serverName, List<Group> groups) { this.serverName = serverName; this.groups = groups; }

    /** Version 1 only: a shape the app was not built for is not guessed at. An
     * entry without a label or with a path that could leave the origin is
     * dropped and the rest is kept; a menu left empty is no menu. */
    static PanelNav parse(String evaluated) {
        if (evaluated == null || evaluated.isEmpty()) return null;
        try {
            Object value = new JSONTokener(evaluated).nextValue();
            if (!(value instanceof JSONObject)) return null;
            JSONObject bridge = (JSONObject) value;
            Object version = bridge.opt("v");
            if (!(version instanceof Number) || ((Number) version).doubleValue() != 1) return null;
            JSONArray nav = bridge.optJSONArray("nav");
            if (nav == null) return null;
            List<Group> groups = new ArrayList<>();
            for (int g = 0; g < nav.length(); g++) {
                JSONObject group = nav.optJSONObject(g);
                if (group == null) continue;
                JSONArray entries = group.optJSONArray("items");
                List<Item> items = new ArrayList<>();
                for (int i = 0; entries != null && i < entries.length(); i++) {
                    JSONObject entry = entries.optJSONObject(i);
                    String label = entry == null ? null : text(entry.opt("label")), path = entry == null ? null : text(entry.opt("path"));
                    if (label == null || !safePath(path)) continue;
                    JSONArray tabEntries = entry.optJSONArray("tabs");
                    List<Link> tabs = new ArrayList<>();
                    for (int t = 0; tabEntries != null && t < tabEntries.length(); t++) {
                        JSONObject tab = tabEntries.optJSONObject(t);
                        String tabLabel = tab == null ? null : text(tab.opt("label")), tabPath = tab == null ? null : text(tab.opt("path"));
                        if (tabLabel != null && safePath(tabPath)) tabs.add(new Link(tabLabel, tabPath));
                    }
                    items.add(new Item(label, path, Collections.unmodifiableList(tabs)));
                }
                if (items.isEmpty()) continue;
                String title = text(group.opt("title"));
                groups.add(new Group(title == null ? "" : title, Collections.unmodifiableList(items)));
            }
            if (groups.isEmpty()) return null;
            JSONObject server = bridge.optJSONObject("server");
            return new PanelNav(server == null ? null : text(server.opt("name")), Collections.unmodifiableList(groups));
        } catch (JSONException e) {
            return null;
        }
    }

    /** A string with something in it, or null. */
    private static String text(Object value) {
        return value instanceof String && !((String) value).trim().isEmpty() ? (String) value : null;
    }

    /** A path on the panel's own origin: one leading slash, and nothing a URL
     * parser could turn into a second one or a line break. */
    static boolean safePath(String path) {
        if (path == null || !path.startsWith("/") || path.startsWith("//")) return false;
        for (int i = 0; i < path.length(); i++) {
            char c = path.charAt(i);
            if (c == '\\' || Character.isISOControl(c)) return false;
        }
        return true;
    }

    /** The server the pages act on, else the saved connection's name. */
    static String title(String serverName, String connectionName, boolean terminal, String terminalWord) {
        String name = serverName == null || serverName.isEmpty() ? connectionName : serverName;
        return terminal ? name + " · " + terminalWord : name;
    }

    /** The page itself or one below it: /services, not /servicesx. */
    static boolean selected(String path, String pagePath) {
        return pagePath != null && (path.equals(pagePath) || pagePath.startsWith(path + "/"));
    }

    /** Whether the page belongs to this item: the item or tab with the longest
     * address that holds the page owns it, so /cluster/running is Running's
     * and not the cluster's. */
    boolean itemSelected(Item item, String pagePath) {
        Item owner = null;
        int length = -1;
        for (Group group : groups) {
            for (Item candidate : group.items) {
                if (selected(candidate.path, pagePath) && candidate.path.length() > length) { owner = candidate; length = candidate.path.length(); }
                for (Link tab : candidate.tabs) {
                    if (selected(tab.path, pagePath) && tab.path.length() > length) { owner = candidate; length = tab.path.length(); }
                }
            }
        }
        return owner == item;
    }

    /** The sheet's buttons, one list per group in order. With no query only
     * the items; with one, the items and the tabs whose label or path holds it. */
    List<List<Hit>> search(String query) {
        String q = query == null ? "" : query.trim().toLowerCase(Locale.ROOT);
        List<List<Hit>> result = new ArrayList<>();
        for (Group group : groups) {
            List<Hit> hits = new ArrayList<>();
            for (Item item : group.items) {
                if (matches(item.label, item.path, q)) hits.add(new Hit(item.label, null, item.path, false, item));
                if (q.isEmpty()) continue;
                for (Link tab : item.tabs) {
                    if (matches(tab.label, tab.path, q)) hits.add(new Hit(item.label + " › " + tab.label, item.label + ", " + tab.label, tab.path, true, item));
                }
            }
            result.add(hits);
        }
        return result;
    }

    private static boolean matches(String label, String path, String query) {
        return (label + " " + path).toLowerCase(Locale.ROOT).contains(query);
    }
}
