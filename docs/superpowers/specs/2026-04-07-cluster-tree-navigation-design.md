# Cluster Tree Navigation Design

## Summary

Replace the current single-sidebar + NodeSelector dropdown with a Proxmox-style dual-panel sidebar when cluster mode is active. Left panel shows a tree (Datacenter > Nodes), right panel shows context menus for the selected item. Single-node (non-cluster) mode keeps the existing sidebar unchanged.

## Problem

When managing many nodes, the current NodeSelector dropdown at the sidebar bottom is hard to navigate. Switching between nodes requires: open dropdown > find node > click > then navigate to the right page. With 10+ nodes, this becomes painful.

## Design

### Activation

- **Cluster disabled**: Existing sidebar, no changes. Everything works as-is.
- **Cluster enabled**: Sidebar switches to dual-panel layout.

### Dual-Panel Layout (Cluster Mode)

```
+------------------+------------------+------------------------+
| Tree Panel       | Context Menu     | Main Content           |
| (~180px)         | (~180px)         |                        |
|                  |                  |                        |
| SFPanel          | DATACENTER       | [page content]         |
|                  |  > Overview      |                        |
| CLUSTER          |  > Nodes         |                        |
|  sfpanel-cluster |  > Tokens        |                        |
|   * Node-1 (L)   |  > Alerts       |                        |
|   * Node-2       |  > Settings      |                        |
|   * Node-3       |                  |                        |
|                  |                  |                        |
| Version / Logout |                  |                        |
+------------------+------------------+------------------------+
```

When a **node** is selected in the tree:

```
+------------------+------------------+------------------------+
| Tree Panel       | Context Menu     | Main Content           |
|                  |                  |                        |
| SFPanel          | NODE-2           | [Node-2 dashboard]     |
|                  |  > Dashboard     |                        |
| CLUSTER          |  > Docker        |                        |
|  sfpanel-cluster |  > Files         |                        |
|   * Node-1 (L)   |  > Cron         |                        |
|   > Node-2 <--   |  > Logs         |                        |
|   * Node-3       |  > Processes     |                        |
|                  |  > Services      |                        |
|                  |  > Network       |                        |
|                  |  > Disk          |                        |
|                  |  > Firewall      |                        |
|                  |  > Packages      |                        |
|                  |  > Terminal      |                        |
+------------------+------------------+------------------------+
```

### Tree Panel (Left, ~180px)

Content:
- Logo / title at top
- **Cluster section**: cluster name as parent node, child nodes listed with status dots (green/yellow/red) and leader badge
- Nodes are expandable/collapsible under the cluster
- **Bottom area**: version info + logout button

Behavior:
- Click cluster name > selects datacenter context > right panel shows datacenter menus
- Click a node > selects node context > right panel shows node-specific menus
- Active item highlighted with primary color
- Status dots update in real-time (15s polling, same as current)

### Context Menu Panel (Right, ~180px)

**When Datacenter selected:**

| Menu Item | Route | Notes |
|-----------|-------|-------|
| Overview | /cluster/overview | Existing cluster overview |
| Nodes | /cluster/nodes | Existing node management |
| Tokens | /cluster/tokens | Existing join tokens |
| Alerts | /settings (alerts tab) | Alert channel/rule config |
| Settings | /settings | Global settings (auth, etc.) |

**When a Node selected:**

| Menu Item | Route | Notes |
|-----------|-------|-------|
| Dashboard | /dashboard | Node's dashboard |
| Docker | /docker | Containers, stacks, images |
| App Store | /appstore | App marketplace |
| Files | /files | File manager |
| Cron | /cron | Cron jobs |
| Logs | /logs | Log viewer |
| Processes | /processes | Process manager |
| Services | /services | systemd services |
| Network | /network | Interfaces, VPN |
| Disk | /disk | Partitions, LVM, RAID |
| Firewall | /firewall | UFW, Fail2ban |
| Packages | /packages | APT, Docker packages |
| Terminal | /terminal | Web terminal |

### Scope Classification

**Global (Datacenter level):**
- Login accounts / authentication / 2FA
- Alert channels and rules
- Cluster management (nodes, tokens, disband)
- App store catalog

**Per-node:**
- Dashboard metrics
- Docker containers/stacks/images/volumes/networks
- File system
- Cron jobs
- Logs
- Processes
- systemd services
- Network interfaces / VPN
- Disk / partitions / LVM / RAID
- Firewall rules
- Packages
- System tuning
- Terminal

### Node Context Switching

When a node is selected in the tree:
1. `api.setCurrentNode(nodeId)` is called (existing mechanism)
2. The context menu panel updates to show node menus
3. The `sfpanel:node-changed` event fires (existing)
4. Main content area re-renders with the selected node's data (existing `key={nodeKey}` mechanism in Layout)

This leverages the existing cluster proxy — all API calls for the selected node are already proxied through the leader to the target node.

### Responsive / Mobile

- On mobile (< md breakpoint), the dual-panel is hidden (same as current sidebar)
- The existing BottomNav + MoreMenu handles mobile navigation
- MoreMenu already has node selector support — no changes needed

### Collapsible State

- Tree panel can be collapsed to icon-only width (~48px) showing just status dots
- Context menu panel collapses with it
- Collapse state saved to localStorage (key: `sfpanel-cluster-sidebar-collapsed`)

## Components

### New Components

1. **`ClusterSidebar.tsx`** — Top-level wrapper that renders TreePanel + ContextMenu side by side. Only rendered when cluster is enabled.

2. **`TreePanel.tsx`** — Left tree panel. Fetches cluster status/nodes, renders the tree. Manages selected item state (datacenter vs specific node).

3. **`ContextMenu.tsx`** — Right menu panel. Receives selected context (datacenter or node) and renders appropriate NavLink items.

### Modified Components

4. **`Layout.tsx`** — Conditionally render `ClusterSidebar` instead of existing `<aside>` sidebar when cluster is enabled. Keep existing sidebar for non-cluster mode.

### State Management

- Selected tree item: `useState` in ClusterSidebar, passed down to TreePanel and ContextMenu
- Cluster status/nodes: fetched in ClusterSidebar, shared via props
- Node switching: uses existing `api.setCurrentNode()` + `sfpanel:node-changed` event

No new global state or context needed.

## Routing

No route changes needed. The existing routes all work as-is because node switching is handled by the API proxy layer (`api.setCurrentNode()`), not by URL params. The dual-panel just provides a better way to trigger node switches and navigate menus.

## Testing

- Visual verification via Playwright screenshots
- Verify node switching works correctly (select node in tree > content updates)
- Verify datacenter menu shows cluster pages
- Verify collapse/expand works and persists
- Verify mobile still works with BottomNav
- Verify non-cluster mode uses existing sidebar
