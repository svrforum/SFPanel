package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func openTestDBForVolUsage(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE docker_volume_usage (
		volume_name TEXT PRIMARY KEY,
		size_bytes  INTEGER NOT NULL,
		measured_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	return db
}

func TestVolumeUsageOnce_WritesCacheRow(t *testing.T) {
	db := openTestDBForVolUsage(t)
	mock := exec.NewMockCommander()
	mock.SetOutput("du", "987654321\t/var/lib/docker/volumes/v1/_data\n", nil)

	measureVolumeUsageOnce(db, mock, func() []*volume.Volume {
		return []*volume.Volume{{Name: "v1"}}
	})

	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM docker_volume_usage`).Scan(&n))
	require.Equal(t, 1, n)
	var sz int64
	require.NoError(t, db.QueryRow(`SELECT size_bytes FROM docker_volume_usage WHERE volume_name='v1'`).Scan(&sz))
	require.Equal(t, int64(987654321), sz)
}

func TestVolumeUsageOnce_DuFailureSkipsVolume(t *testing.T) {
	db := openTestDBForVolUsage(t)
	mock := exec.NewMockCommander()
	mock.SetOutput("du", "", errFakeNoSuchFile)

	measureVolumeUsageOnce(db, mock, func() []*volume.Volume {
		return []*volume.Volume{{Name: "v1"}}
	})

	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM docker_volume_usage`).Scan(&n))
	require.Equal(t, 0, n)
}

var errFakeNoSuchFile = errFake("du: cannot access /var/lib/...: No such file or directory")

type errFake string

func (e errFake) Error() string { return string(e) }
