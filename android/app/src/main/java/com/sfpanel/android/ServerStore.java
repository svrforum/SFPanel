package com.sfpanel.android;

import android.content.SharedPreferences;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;
import java.util.ArrayList;
import java.util.List;

final class ServerStore {
    record Server(String name, String address) {}
    private final SharedPreferences prefs;

    ServerStore(SharedPreferences prefs) { this.prefs = prefs; }

    List<Server> list() {
        List<Server> result = new ArrayList<>();
        try {
            JSONArray saved = new JSONArray(prefs.getString("servers", "[]"));
            for (int i = 0; i < saved.length(); i++) {
                JSONObject row = saved.getJSONObject(i);
                try {
                    result.add(new Server(row.getString("name"), ServerAddress.normalize(row.getString("address"))));
                } catch (IllegalArgumentException ignored) { /* Ignore obsolete or corrupt entries. */ }
            }
        } catch (JSONException ignored) { /* No secrets or user data beyond connection bookmarks. */ }
        return result;
    }

    void save(Server server) {
        List<Server> servers = list();
        servers.removeIf(s -> s.address().equals(server.address()));
        servers.add(0, server);
        write(servers.subList(0, Math.min(20, servers.size())));
    }

    void remove(Server server) {
        List<Server> servers = list();
        servers.removeIf(s -> s.address().equals(server.address()));
        write(servers);
    }

    private void write(List<Server> servers) {
        JSONArray rows = new JSONArray();
        for (Server s : servers) {
            JSONObject row = new JSONObject();
            try {
                row.put("name", s.name());
                row.put("address", s.address());
                rows.put(row);
            } catch (JSONException e) { throw new IllegalStateException(e); }
        }
        prefs.edit().putString("servers", rows.toString()).apply();
    }
}
