package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withUnitPath temporarily points the package-level unitPath at p for the
// duration of a single test.
func withUnitPath(t *testing.T, p string) {
	t.Helper()
	prev := unitPath
	unitPath = p
	t.Cleanup(func() { unitPath = prev })
}

// stubDaemonReload replaces the real `systemctl daemon-reload` with a no-op
// for the duration of a test and returns a pointer to a flag reporting
// whether it was invoked. Without this, tests that trigger a migration block
// on the real systemctl timeout when no systemd is running.
func stubDaemonReload(t *testing.T) *bool {
	t.Helper()
	called := false
	prev := daemonReload
	daemonReload = func() error { called = true; return nil }
	t.Cleanup(func() { daemonReload = prev })
	return &called
}

func TestMigrateRestartPolicy_RewritesOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nType=simple\nExecStart=/usr/local/bin/sfpanel\nRestart=on-failure\nRestartSec=5\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)
	reloaded := stubDaemonReload(t)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true for Restart=on-failure unit")
	}
	if !*reloaded {
		t.Error("expected daemon-reload to be issued after a successful migration")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Restart=always") {
		t.Errorf("unit file was not rewritten:\n%s", got)
	}
	if strings.Contains(string(got), "Restart=on-failure") {
		t.Errorf("Restart=on-failure still present after migration:\n%s", got)
	}
	// Make sure we didn't lose unrelated lines.
	if !strings.Contains(string(got), "ExecStart=/usr/local/bin/sfpanel") {
		t.Errorf("unrelated directive lost during migration:\n%s", got)
	}
}

func TestMigrateRestartPolicy_AlreadyMigratedIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nRestart=always\nRestartSec=5\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false for already-Restart=always unit")
	}

	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("unit file was modified even though migration was a noop:\n%s", got)
	}
}

func TestMigrateRestartPolicy_MissingUnitIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service") // intentionally not created
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error for missing unit: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false when no unit file exists")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("MigrateRestartPolicy should not create the unit file from scratch")
	}
}

// withSystemdRuntimePath temporarily points the package-level
// systemdRuntimePath at p for the duration of a single test.
func withSystemdRuntimePath(t *testing.T, p string) {
	t.Helper()
	prev := systemdRuntimePath
	systemdRuntimePath = p
	t.Cleanup(func() { systemdRuntimePath = prev })
}

func TestIsSystemdActive_DirectoryPresent(t *testing.T) {
	dir := t.TempDir()
	withSystemdRuntimePath(t, dir)
	if !IsSystemdActive() {
		t.Error("expected IsSystemdActive=true when runtime dir exists")
	}
}

func TestIsSystemdActive_Missing(t *testing.T) {
	dir := t.TempDir()
	withSystemdRuntimePath(t, filepath.Join(dir, "does-not-exist"))
	if IsSystemdActive() {
		t.Error("expected IsSystemdActive=false when runtime dir is absent")
	}
}

func TestIsSystemdActive_FileNotDir(t *testing.T) {
	// A file at /run/systemd/system would be very unusual but should not
	// be treated as a live systemd — sd_booted explicitly checks for a
	// directory.
	dir := t.TempDir()
	path := filepath.Join(dir, "imposter")
	if err := os.WriteFile(path, []byte("not systemd"), 0644); err != nil {
		t.Fatal(err)
	}
	withSystemdRuntimePath(t, path)
	if IsSystemdActive() {
		t.Error("expected IsSystemdActive=false when path is a regular file")
	}
}

func TestMigrateRestartPolicy_UnknownRestartValueLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sfpanel.service")
	original := "[Service]\nRestart=on-abort\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)

	migrated, err := MigrateRestartPolicy()
	if err != nil {
		t.Fatalf("MigrateRestartPolicy returned error: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false for unknown Restart= value")
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("unit file with unknown Restart= was rewritten — we should not overrule the operator:\n%s", got)
	}
}

// The unit install.sh wrote up to v0.78.11, hardening block included.
const privateTmpUnit = `[Service]
ExecStart=/usr/local/bin/sfpanel /etc/sfpanel/config.yaml
Restart=always
# breaking features. The few that *are* safe:
# - PrivateTmp: panel doesn't share /tmp with other services
# - RestrictSUIDSGID: panel never creates suid/sgid files
NoNewPrivileges=false
PrivateTmp=true
RestrictSUIDSGID=true
`

func TestMigratePrivateTmp_DropsWhatInstallShWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfpanel.service")
	if err := os.WriteFile(path, []byte(privateTmpUnit), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)
	reloaded := stubDaemonReload(t)

	migrated, err := MigratePrivateTmp()
	if err != nil || !migrated {
		t.Fatalf("MigratePrivateTmp() = %v, %v; want true, nil", migrated, err)
	}
	if !*reloaded {
		t.Error("expected daemon-reload after the unit changed")
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(strings.Replace(privateTmpUnit,
		"# - PrivateTmp: panel doesn't share /tmp with other services\n", "", 1),
		"PrivateTmp=true\n", "", 1)
	if string(got) != want {
		t.Errorf("unit after migration:\n%s\nwant:\n%s", got, want)
	}

	// A second run finds nothing to do.
	if migrated, err := MigratePrivateTmp(); err != nil || migrated {
		t.Errorf("second MigratePrivateTmp() = %v, %v; want false, nil", migrated, err)
	}
}

func TestMigratePrivateTmp_LeavesAnOperatorsSettingAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfpanel.service")
	unit := "[Service]\nExecStart=/usr/local/bin/sfpanel\nPrivateTmp=yes\n"
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)
	reloaded := stubDaemonReload(t)

	if migrated, err := MigratePrivateTmp(); err != nil || migrated {
		t.Fatalf("MigratePrivateTmp() = %v, %v; want false, nil", migrated, err)
	}
	if got, _ := os.ReadFile(path); string(got) != unit || *reloaded {
		t.Errorf("an operator's PrivateTmp=yes was touched:\n%s", got)
	}
}

func TestMigratePrivateTmp_MissingUnitIsNoop(t *testing.T) {
	withUnitPath(t, filepath.Join(t.TempDir(), "absent.service"))
	stubDaemonReload(t)
	if migrated, err := MigratePrivateTmp(); err != nil || migrated {
		t.Fatalf("MigratePrivateTmp() = %v, %v; want false, nil", migrated, err)
	}
}

// The unit install.sh writes from v0.78.12, after PrivateTmp left it.
const restrictSUIDSGIDUnit = `[Service]
ExecStart=/usr/local/bin/sfpanel /etc/sfpanel/config.yaml
Restart=always
# breaking features. The one that *is* safe:
# - RestrictSUIDSGID: panel never creates suid/sgid files
# Notably NOT enabled:
# - PrivateTmp: gives the service a mount namespace of its own, so the
#   disks and network shares it mounts never reach the host or Docker
NoNewPrivileges=false
RestrictSUIDSGID=true

[Install]
WantedBy=multi-user.target
`

// Both shapes install.sh ever wrote lose the directive and its comment line,
// and nothing else: the PrivateTmp lines of the older unit are the other
// migration's to drop.
func TestMigrateRestrictSUIDSGID_DropsWhatInstallShWrote(t *testing.T) {
	for name, unit := range map[string]string{
		"v0.13.1 to v0.78.11": privateTmpUnit,
		"v0.78.12 on":         restrictSUIDSGIDUnit,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sfpanel.service")
			if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
				t.Fatal(err)
			}
			withUnitPath(t, path)
			reloaded := stubDaemonReload(t)

			migrated, err := MigrateRestrictSUIDSGID()
			if err != nil || !migrated {
				t.Fatalf("MigrateRestrictSUIDSGID() = %v, %v; want true, nil", migrated, err)
			}
			if !*reloaded {
				t.Error("expected daemon-reload after the unit changed")
			}
			got, _ := os.ReadFile(path)
			want := strings.Replace(strings.Replace(unit,
				"# - RestrictSUIDSGID: panel never creates suid/sgid files\n", "", 1),
				"RestrictSUIDSGID=true\n", "", 1)
			if string(got) != want {
				t.Errorf("unit after migration:\n%s\nwant:\n%s", got, want)
			}
			if strings.Contains(string(got), "RestrictSUIDSGID") {
				t.Errorf("RestrictSUIDSGID is still in the unit:\n%s", got)
			}

			if migrated, err := MigrateRestrictSUIDSGID(); err != nil || migrated {
				t.Errorf("second MigrateRestrictSUIDSGID() = %v, %v; want false, nil", migrated, err)
			}
		})
	}
}

// At boot both migrations run, one after the other, on the oldest unit.
func TestMigrateRestrictSUIDSGID_AndPrivateTmpTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfpanel.service")
	if err := os.WriteFile(path, []byte(privateTmpUnit), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)
	stubDaemonReload(t)

	if _, err := MigratePrivateTmp(); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRestrictSUIDSGID(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	for _, gone := range []string{"PrivateTmp", "RestrictSUIDSGID"} {
		if strings.Contains(string(got), gone) {
			t.Errorf("%s is still in the unit:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"ExecStart=/usr/local/bin/sfpanel /etc/sfpanel/config.yaml", "Restart=always", "NoNewPrivileges=false"} {
		if !strings.Contains(string(got), kept) {
			t.Errorf("%q was lost:\n%s", kept, got)
		}
	}
}

func TestMigrateRestrictSUIDSGID_LeavesAnOperatorsSettingAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sfpanel.service")
	unit := "[Service]\nExecStart=/usr/local/bin/sfpanel\nRestrictSUIDSGID=yes\n"
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		t.Fatal(err)
	}
	withUnitPath(t, path)
	reloaded := stubDaemonReload(t)

	if migrated, err := MigrateRestrictSUIDSGID(); err != nil || migrated {
		t.Fatalf("MigrateRestrictSUIDSGID() = %v, %v; want false, nil", migrated, err)
	}
	if got, _ := os.ReadFile(path); string(got) != unit || *reloaded {
		t.Errorf("an operator's RestrictSUIDSGID=yes was touched:\n%s", got)
	}
}

func TestMigrateRestrictSUIDSGID_MissingUnitIsNoop(t *testing.T) {
	withUnitPath(t, filepath.Join(t.TempDir(), "absent.service"))
	stubDaemonReload(t)
	if migrated, err := MigrateRestrictSUIDSGID(); err != nil || migrated {
		t.Fatalf("MigrateRestrictSUIDSGID() = %v, %v; want false, nil", migrated, err)
	}
}
