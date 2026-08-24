package disk

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func deleteReq(t *testing.T, device, number string) (*httptest.ResponseRecorder, echo.Context) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/partitions/"+device+"/"+number, nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("device", "number")
	c.SetParamValues(device, number)
	return rec, c
}

// Format refuses a device mounted at a protected path; delete did not, though
// it is the worse operation — it takes the partition table entry with the
// contents. Deleting the partition behind /boot leaves a host that does not
// come back.
func TestDeletePartitionRefusesProtectedMount(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(dev string) (string, error) {
		if dev != "/dev/sda1" {
			t.Errorf("checked %q; the guard must look at the partition, not the disk", dev)
		}
		return "/boot", nil
	}

	rec, c := deleteReq(t, "sda", "1")
	h := &Handler{Cmd: partedAvailable()}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// The reason, not just the refusal: a missing parted binary also answers
	// with an error, and that would satisfy a status-only assertion.
	if body := rec.Body.String(); !contains(body, "protected system path") {
		t.Errorf("body = %s, want the protected-path reason", body)
	}
}

// An ordinary mounted partition is not a protected path, but deleting it out
// from under a running mount is still not something to do silently.
func TestDeletePartitionRefusesAnyMountedPartition(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "/mnt/data", nil }

	rec, c := deleteReq(t, "sdb", "2")
	h := &Handler{Cmd: partedAvailable()}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "unmount it first") {
		t.Errorf("body = %s, want the unmount instruction", body)
	}
}

// An unmounted partition still deletes.
func TestDeletePartitionProceedsWhenUnmounted(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }

	rec, c := deleteReq(t, "sdb", "2")
	h := &Handler{Cmd: partedAvailable()}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// parted must look installed, or the handler's tool check short-circuits
// before the guard under test and every assertion here would pass on a 503.
func partedAvailable() *exec.MockCommander {
	return &exec.MockCommander{
		Outputs: map[string]exec.MockResult{"exists:parted": {}},
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
