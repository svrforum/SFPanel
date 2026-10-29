package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

// agentCommand runs `sfpanel agent …`: the commands of a server that is, or
// is to become, an agent of a cluster. None of them asks anything — install.sh
// runs `agent join` with the script itself on stdin.
func agentCommand(args []string) {
	if os.Getuid() != 0 {
		log.Fatal("SFPanel must be run as root. Use: sudo sfpanel agent <command>")
	}
	if len(args) == 0 {
		printAgentHelp()
		return
	}
	switch args[0] {
	case "join":
		agentJoin(args[1:])
	case "status":
		agentStatusCommand(args[1:])
	case "reset":
		agentResetCommand(args[1:])
	case "help":
		printAgentHelp()
	default:
		fmt.Printf("Unknown agent command: %s\n", args[0])
		printAgentHelp()
		os.Exit(1)
	}
}

const agentJoinUsage = "Usage: sfpanel agent join <manager-address:port> <token> --ca <fingerprint> [--advertise IP] [--name NAME] [--config PATH] [--no-restart]"

// agentJoinArgs are the arguments of `sfpanel agent join`.
type agentJoinArgs struct {
	Manager, Token, Pin, Advertise, ConfigPath, Name string
	NoRestart                                        bool
}

// parseAgentJoinArgs reads `agent join`'s arguments: the manager's gRPC
// address and the token, and the options in any place. Whatever it can tell
// is wrong it refuses here, before anything is sent: an option it does not
// know (install.sh passes arguments through, and a misspelt one skipped
// would join with the wrong address), the fingerprint's form, a name no node
// may have, and an address with no port — none is guessed, the command
// `cluster token --agent` prints names it.
func parseAgentJoinArgs(args []string) (agentJoinArgs, error) {
	a := agentJoinArgs{ConfigPath: defaultCfgPath}
	var positional []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--no-restart":
			a.NoRestart = true
		case "--ca", "--advertise", "--config", "--name":
			if i+1 == len(args) {
				return agentJoinArgs{}, fmt.Errorf("%s needs a value", arg)
			}
			i++
			switch arg {
			case "--ca":
				a.Pin = args[i]
			case "--advertise":
				a.Advertise = args[i]
			case "--name":
				if err := cluster.ValidNodeName(args[i]); err != nil {
					return agentJoinArgs{}, err
				}
				a.Name = args[i]
			default:
				a.ConfigPath = args[i]
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return agentJoinArgs{}, fmt.Errorf("unknown option %s", arg)
			}
			positional = append(positional, arg)
		}
	}
	switch len(positional) {
	case 0:
		return agentJoinArgs{}, errors.New("the manager's address and the join token are missing")
	case 1:
		return agentJoinArgs{}, errors.New("the join token is missing")
	case 2:
	default:
		return agentJoinArgs{}, fmt.Errorf("unexpected argument %q", positional[2])
	}
	a.Manager, a.Token = positional[0], positional[1]
	if a.Pin == "" {
		return agentJoinArgs{}, errors.New("--ca is missing: copy the whole command 'sfpanel cluster token --agent' prints on a manager, fingerprint included")
	}
	pin, err := cluster.NormalizeCAPin(a.Pin)
	if err != nil {
		return agentJoinArgs{}, err
	}
	a.Pin = pin
	host, port, err := net.SplitHostPort(a.Manager)
	if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return agentJoinArgs{}, fmt.Errorf("the manager address %q needs a port: the manager's cluster gRPC port, as in the command 'sfpanel cluster token --agent' prints", a.Manager)
	}
	return a, nil
}

// agentJoin runs `sfpanel agent join`. The service is restarted only once the
// config names this server an agent: the panel that comes up then takes only
// what its managers sign, and has no login or setup screen to leave open.
func agentJoin(args []string) {
	a, err := parseAgentJoinArgs(args)
	if err != nil {
		fmt.Println(agentJoinUsage)
		log.Fatal(err)
	}
	// Created when missing, as 'cluster join' does.
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	engine, panelCA, tlsTurnedOn, err := prepareAgentJoin(cfg, a)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Joining the cluster at %s as an agent...\n", a.Manager)
	res, err := engine.Execute(a.Manager, a.Token, a.Advertise, a.Pin, panelCA)
	if err != nil {
		log.Fatalf("Agent join failed: %v", err)
	}

	fmt.Printf("\nJoined cluster '%s' as an agent.\n", res.ClusterName)
	fmt.Printf("Node ID: %s\n", res.NodeID)
	fmt.Println("Managers:")
	for _, m := range res.Managers {
		fmt.Printf("  %s  %s\n", m.ID, m.GRPCAddress)
	}
	if tlsTurnedOn {
		fmt.Printf("Panel HTTPS is on now, with a certificate from a local CA in %s: managers reach an agent's panel over HTTPS only.\n", cfg.Server.TLS.Dir)
	}
	if a.NoRestart {
		fmt.Println("\nOnce sfpanel restarts, the panel on this server answers its managers only.")
		return
	}
	restarted, err := restartIfActive()
	switch {
	case err != nil:
		log.Fatalf("The join is done, but sfpanel did not restart: %v — see 'journalctl -u sfpanel', then run '%ssystemctl restart sfpanel'", err, sudoPrefix())
	case restarted:
		fmt.Println("\nRestarted sfpanel: the panel on this server now answers its managers only.")
	default:
		fmt.Printf("\nsfpanel is not running; once it starts (%ssystemctl start sfpanel), the panel on this server answers its managers only.\n", sudoPrefix())
	}
}

// prepareAgentJoin readies this server for `agent join` and returns the
// engine that runs it, with the CA behind the panel's certificate and whether
// the panel's TLS was turned on: a server in a cluster, or one with a
// manager's files, is refused first, and the panel's HTTPS is paneltls's to
// prepare and to check, as it is for a manager's conversion to an agent.
func prepareAgentJoin(cfg *config.Config, a agentJoinArgs) (*cluster.AgentJoinEngine, []byte, bool, error) {
	if err := readyForAgentJoin(cfg); err != nil {
		return nil, nil, false, err
	}
	panelCA, tlsTurnedOn, err := paneltls.PrepareForAgent(cfg)
	if err != nil {
		return nil, nil, false, err
	}
	engine := &cluster.AgentJoinEngine{Config: cfg, ConfigPath: a.ConfigPath, Version: version, Name: a.Name, CheckPanelCert: paneltls.CertCheck(cfg)}
	return engine, panelCA, tlsTurnedOn, nil
}

// readyForAgentJoin refuses a join on a server in a cluster already — an
// agent config boots as an agent whatever cluster.enabled says (main) — or on
// one that still holds a cluster manager's files, and removes the state an
// earlier membership as an agent left behind: the link would prefer it to the
// managers the join registers with, and it may record that membership's
// removal, which keeps an agent on loopback.
func readyForAgentJoin(cfg *config.Config) error {
	switch {
	case cfg.Cluster.IsAgent():
		return errors.New("this server is an agent already: 'sfpanel agent status' shows its link, and after 'sfpanel agent reset --yes' it can join again")
	case cfg.Cluster.Enabled:
		return errors.New("this server is a cluster manager: 'sfpanel cluster leave' takes it out of the cluster before it can join as an agent")
	}
	// A cluster manager's files hold the cluster's keys — the CA key, every
	// agent's key, the accounts — and an agent joined over them kept them.
	var leftovers []string
	for _, p := range cluster.ManagerFiles(&cfg.Cluster) {
		if _, err := os.Lstat(p); err == nil {
			leftovers = append(leftovers, p)
		}
	}
	if len(leftovers) > 0 {
		return fmt.Errorf("this server still holds a cluster manager's files, which hold the cluster's keys: %s — an agent needs none of them; remove them, then join again", strings.Join(leftovers, ", "))
	}
	if cfg.Cluster.DataDir == "" {
		return nil
	}
	path := agentStatePath(cfg)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s, the state of an earlier membership: %w", path, err)
	}
	return nil
}

// runSystemctl runs systemctl; tests replace it.
var runSystemctl = func(args ...string) error {
	return exec.Command("systemctl", args...).Run()
}

// restartIfActive restarts the sfpanel service when it is running, and
// reports whether it did. One that is not — a first install that has not set
// up its unit yet, a container — starts from the new config on its own.
func restartIfActive() (bool, error) {
	if runSystemctl("is-active", "--quiet", "sfpanel") != nil {
		return false, nil
	}
	return true, runSystemctl("restart", "sfpanel")
}

// agentStatePath is where an agent's link keeps the managers and the leader
// it last heard of, and whether the leader removed it.
func agentStatePath(cfg *config.Config) string {
	return filepath.Join(cfg.Cluster.DataDir, cluster.AgentStateFile)
}

// notAnAgent says what a server that is not an agent is instead.
func notAnAgent(cfg *config.Config) error {
	if cfg.Cluster.Enabled {
		return errors.New("this server is not an agent but a cluster manager: 'sfpanel cluster status' shows its cluster")
	}
	return errors.New("this server is not an agent: it runs as a standalone panel")
}

func agentStatusCommand(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	if len(rest) > 0 {
		fmt.Println("Usage: sfpanel agent status [--config PATH]")
		os.Exit(1)
	}
	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.IsAgent() {
		fmt.Println(notAnAgent(cfg))
		return
	}
	writeAgentStatus(os.Stdout, cfg)
}

// writeAgentStatus reports an agent from its own files and its panel's
// loopback health. Unlike `cluster status` it mints no JWT: an agent's panel
// takes none.
func writeAgentStatus(w io.Writer, cfg *config.Config) {
	c := cfg.Cluster
	fmt.Fprintln(w, "Role: agent")
	fmt.Fprintf(w, "Cluster: %s\n", c.Name)
	fmt.Fprintf(w, "Node ID: %s\n", c.NodeID)
	fmt.Fprintf(w, "Node Name: %s\n", c.NodeName)
	fmt.Fprintf(w, "Advertise: %s\n", c.AdvertiseAddress)

	// Once the link has saved what the leader said, it goes by that, not by
	// the managers the config names.
	path := agentStatePath(cfg)
	source := "the leader's last heartbeat reply"
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		source = "the registration; no heartbeat reply saved yet"
	}
	st, err := cluster.LoadAgentState(path, c.Managers)
	if err != nil {
		fmt.Fprintf(w, "State: %v\n", err)
		st, source = &cluster.AgentState{Managers: c.Managers}, "the registration"
	}
	fmt.Fprintf(w, "Managers (from %s):\n", source)
	for _, m := range st.Managers {
		fmt.Fprintf(w, "  %s  %s\n", m.ID, m.GRPCAddress)
	}
	leader := st.Leader
	if leader == "" {
		leader = "none heard from yet"
	}
	fmt.Fprintf(w, "Leader: %s\n", leader)
	if st.Removed {
		fmt.Fprintln(w, "Removed: yes — the leader removed this server from the cluster; 'sfpanel agent reset --yes' makes it a standalone panel again")
	} else {
		fmt.Fprintln(w, "Removed: no")
	}

	tm := cluster.NewTLSManager(c.CertDir)
	// By pin, as join commands name a CA: two while a CA rotation is under
	// way, and the certificate names the one that signed it.
	cas, err := trustedCAs(tm)
	if err != nil {
		fmt.Fprintf(w, "Trusted CAs: %v\n", err)
	} else {
		pins := make([]string, 0, len(cas))
		for _, ca := range cas {
			pins = append(pins, cluster.CAPin(ca.RawSubjectPublicKeyInfo))
		}
		fmt.Fprintf(w, "Trusted CAs: %s\n", strings.Join(pins, ", "))
	}
	switch leaf, err := tm.NodeCertLeaf(); {
	case err != nil:
		fmt.Fprintf(w, "Certificate: %v\n", err)
	case time.Now().After(leaf.NotAfter):
		fmt.Fprintf(w, "Certificate: expired %s, %s\n", leaf.NotAfter.Format("2006-01-02"), certIssuer(leaf, cas))
	default:
		fmt.Fprintf(w, "Certificate: valid until %s, %s\n", leaf.NotAfter.Format("2006-01-02"), certIssuer(leaf, cas))
	}
	// Whether the key is there; never the key.
	if _, err := tm.LoadAgentKey(); err != nil {
		fmt.Fprintf(w, "Agent key: %v\n", err)
	} else {
		fmt.Fprintln(w, "Agent key: present")
	}
	fmt.Fprintf(w, "Panel: %s\n", panelHealth(cfg))
}

// trustedCAs are the CAs in the agent's ca.crt, taken as its trust pool
// takes them: a block that is not a certificate, has headers or does not
// parse is skipped. A ca.crt that cannot be read, or holds no certificate the
// pool would take, is an error.
func trustedCAs(tm *cluster.TLSManager) ([]*x509.Certificate, error) {
	data, err := tm.LoadCACert()
	if err != nil {
		return nil, err
	}
	var cas []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		if ca, err := x509.ParseCertificate(block.Bytes); err == nil {
			cas = append(cas, ca)
		}
	}
	if len(cas) == 0 {
		return nil, errors.New("ca.crt holds no certificate")
	}
	return cas, nil
}

// certIssuer names the CA among cas that signed leaf.
func certIssuer(leaf *x509.Certificate, cas []*x509.Certificate) string {
	for _, ca := range cas {
		if leaf.CheckSignatureFrom(ca) == nil {
			return "from CA " + cluster.CAPin(ca.RawSubjectPublicKeyInfo)
		}
	}
	return "from a CA this agent does not trust"
}

// panelHealth asks the panel's public health route over loopback, which a
// removed agent still serves.
func panelHealth(cfg *config.Config) string {
	self := selfPanel(cfg)
	target := self.URL("/api/v1/health")
	client, err := self.HTTPClient(5 * time.Second)
	if err != nil {
		return fmt.Sprintf("cannot check %s: %v", target, err)
	}
	resp, err := client.Get(target)
	if err != nil {
		var uerr *url.Error // names the URL again
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Sprintf("not answering at %s: %v", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("answering at %s, unhealthy: %s", target, resp.Status)
	}
	var body struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil || body.Data.Version == "" {
		return fmt.Sprintf("answering at %s", target)
	}
	return fmt.Sprintf("answering at %s (version %s)", target, body.Data.Version)
}

func agentResetCommand(args []string) {
	cfgPath, rest := parseCfgFlag(args)
	yes := false
	for _, arg := range rest {
		if arg != "--yes" {
			fmt.Println("Usage: sfpanel agent reset --yes [--config PATH]")
			os.Exit(1)
		}
		yes = true
	}
	cfg := loadCfgForCLI(cfgPath)
	if !cfg.Cluster.IsAgent() {
		log.Fatal(notAnAgent(cfg))
	}
	if !yes {
		fmt.Print(agentResetPlan(cfg, cfgPath))
		fmt.Println("\nNothing was changed. Run 'sfpanel agent reset --yes' to go ahead.")
		os.Exit(1)
	}
	nodeID, removed := cfg.Cluster.NodeID, removedByLeader(cfg)
	if err := agentReset(cfg, cfgPath); err != nil {
		log.Fatalf("Agent reset: %v", err)
	}
	if !removed {
		fmt.Printf("The cluster still lists this server as node %s: remove it on a manager with 'sfpanel cluster remove %s'.\n", nodeID, nodeID)
	}
}

// agentReset makes an agent a standalone panel again: the config loses its
// cluster block and gets a new JWT key, the files the join wrote and the
// link's state go, and a running service restarts. The cluster keeps its
// record of the node until a manager removes it — an agent cannot take
// itself out.
func agentReset(cfg *config.Config, cfgPath string) error {
	if !cfg.Cluster.IsAgent() {
		return notAnAgent(cfg)
	}
	files := agentResetFiles(cfg)
	next := *cfg
	next.Cluster.Enabled = false
	next.Cluster.Role = ""
	next.Cluster.NodeID = ""
	next.Cluster.Managers = nil
	// The panel takes logins from now on, and while this server was an agent
	// its managers could read config.yaml, the old key with it, through the
	// file manager.
	next.Auth.JWTSecret = config.NewSecret()
	// The config first: an agent config whose key is gone stops every boot —
	// an agent never falls back to a standalone panel — while files left
	// beside a standalone config do nothing.
	if err := saveConfig(cfgPath, &next); err != nil {
		return fmt.Errorf("save the config: %w", err)
	}
	*cfg = next

	var left []error
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			left = append(left, err)
		}
	}
	restarted, restartErr := restartIfActive()
	switch {
	case restartErr != nil:
		left = append(left, fmt.Errorf("restart sfpanel: %w — run 'systemctl restart sfpanel'", restartErr))
	case restarted:
		fmt.Println("Restarted sfpanel: this server is a standalone panel again.")
	default:
		fmt.Println("This server is a standalone panel again once sfpanel starts.")
	}
	if len(left) > 0 {
		return fmt.Errorf("the config is a standalone panel's now, but: %w", errors.Join(left...))
	}
	return nil
}

// agentResetFiles are what a reset removes: the files the join wrote in the
// cert dir, and the link's state.
func agentResetFiles(cfg *config.Config) []string {
	var paths []string
	if dir := cfg.Cluster.CertDir; dir != "" {
		for _, f := range []string{"ca.crt", "node.crt", "node.key", "agent.key", cluster.AgentKeyFile} {
			paths = append(paths, filepath.Join(dir, f))
		}
	}
	if cfg.Cluster.DataDir != "" {
		paths = append(paths, agentStatePath(cfg))
	}
	return paths
}

// agentResetPlan is what `agent reset` without --yes shows: what --yes does.
func agentResetPlan(cfg *config.Config, cfgPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "'sfpanel agent reset --yes' makes this agent (node %s) a standalone panel again. It:\n", cfg.Cluster.NodeID)
	for _, p := range agentResetFiles(cfg) {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(&b, "  removes %s\n", p)
		}
	}
	fmt.Fprintf(&b, "  clears the cluster block of %s and gives the panel a new JWT key\n", cfgPath)
	b.WriteString("  restarts sfpanel if it is running\n")
	if !removedByLeader(cfg) {
		fmt.Fprintf(&b, "The cluster keeps its record of the node until a manager removes it ('sfpanel cluster remove %s').\n", cfg.Cluster.NodeID)
	}
	return b.String()
}

// removedByLeader reports whether the link recorded that the leader removed
// this agent, so the cluster lists it no more.
func removedByLeader(cfg *config.Config) bool {
	st, err := cluster.LoadAgentState(agentStatePath(cfg), nil)
	return err == nil && st.Removed
}

func printAgentHelp() {
	fmt.Println("SFPanel Agent Commands:")
	fmt.Println()
	fmt.Println("  sfpanel agent join ADDR:PORT TOKEN --ca FINGERPRINT [--advertise IP] [--name NAME] [--no-restart]")
	fmt.Println("                                            Join a cluster as an agent ('sfpanel cluster token --agent' on a manager prints the command)")
	fmt.Println("  sfpanel agent status                      Show this agent's managers, certificate and panel")
	fmt.Println("  sfpanel agent reset [--yes]               Make this agent a standalone panel again; without --yes, show what that removes")
	fmt.Println()
	fmt.Println("All subcommands accept --config PATH to select a config file")
	fmt.Println("(default: /etc/sfpanel/config.yaml).")
}
