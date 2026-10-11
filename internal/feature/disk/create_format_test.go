package disk

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// stubPartitionNumbers stands in for the kernel's partition list: the first
// call sees before, every later one after.
func stubPartitionNumbers(t *testing.T, before, after []int) *int {
	t.Helper()
	orig := partitionNumbers
	t.Cleanup(func() { partitionNumbers = orig })
	calls := 0
	partitionNumbers = func(string) (map[int]bool, error) {
		calls++
		list := after
		if calls == 1 {
			list = before
		}
		set := map[int]bool{}
		for _, n := range list {
			set[n] = true
		}
		return set, nil
	}
	return &calls
}

func createPartition(t *testing.T, cmd *exec.MockCommander, device, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/disks/"+device+"/partitions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("device")
	c.SetParamValues(device)
	if err := (&Handler{Cmd: cmd}).CreatePartition(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

func toolsAvailable(names ...string) *exec.MockCommander {
	cmd := &exec.MockCommander{Outputs: map[string]exec.MockResult{}}
	for _, n := range names {
		cmd.Outputs["exists:"+n] = exec.MockResult{}
	}
	return cmd
}

// callNames lists the commands run, leaving out the tool lookups.
func callNames(calls []exec.MockCall) []string {
	var names []string
	for _, c := range calls {
		if !strings.HasPrefix(c.Name, "exists:") {
			names = append(names, c.Name)
		}
	}
	return names
}

func findCall(calls []exec.MockCall, name string) *exec.MockCall {
	for i := range calls {
		if calls[i].Name == name {
			return &calls[i]
		}
	}
	return nil
}

// The partition dialog's file system choice was only the type parted records,
// so the new partition came out empty while the dialog read as though it had
// been formatted (#113). With format it is formatted, on the partition the
// kernel added, named the kernel's way.
func TestCreatePartitionFormatsTheNewPartition(t *testing.T) {
	for _, tc := range []struct {
		device, fsType, tool string
		wantArgs             []string
	}{
		{"vdb", "ext4", "mkfs.ext4", []string{"-F", "/dev/vdb2"}},
		{"nvme0n1", "ext4", "mkfs.ext4", []string{"-F", "/dev/nvme0n1p2"}},
		{"vdb", "swap", "mkswap", []string{"/dev/vdb2"}},
	} {
		stubPartitionNumbers(t, []int{1}, []int{1, 2})
		cmd := toolsAvailable("parted", "udevadm", tc.tool)
		rec := createPartition(t, cmd, tc.device, `{"start":"50%","end":"100%","fs_type":"`+tc.fsType+`","format":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.device, tc.fsType, rec.Code, rec.Body.String())
		}
		call := findCall(cmd.Calls, tc.tool)
		if call == nil || !reflect.DeepEqual(call.Args, tc.wantArgs) {
			t.Errorf("%s %s: ran %v, want %s %v", tc.device, tc.fsType, cmd.Calls, tc.tool, tc.wantArgs)
		}
		// The new partition's device node is udev's to finish before mkfs
		// opens it, and the list read after the answer must see the format.
		if got, want := callNames(cmd.Calls), []string{"parted", "udevadm", tc.tool, "udevadm"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s %s: order %v, want %v", tc.device, tc.fsType, got, want)
		}
	}
}

func TestCreatePartitionWithoutFormatLeavesItEmpty(t *testing.T) {
	calls := stubPartitionNumbers(t, []int{1}, []int{1, 2})
	cmd := toolsAvailable("parted", "udevadm", "mkfs.ext4")
	rec := createPartition(t, cmd, "vdb", `{"start":"50%","end":"100%","fs_type":"ext4"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	assertNotRun(t, cmd, "mkfs.ext4")
	if *calls != 0 {
		t.Errorf("listed the partitions %d times for a create without format", *calls)
	}
}

// mkfs.xfs is not on a stock Ubuntu. Finding that out after parted would
// leave a partition nobody asked for in that state.
func TestCreatePartitionChecksTheFormatToolBeforeTouchingTheDisk(t *testing.T) {
	stubPartitionNumbers(t, []int{1}, []int{1, 2})
	cmd := toolsAvailable("parted", "udevadm")
	rec := createPartition(t, cmd, "vdb", `{"start":"50%","end":"100%","fs_type":"xfs","format":true}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), response.ErrToolNotInstalled) {
		t.Errorf("got %d %s, want 503 %s", rec.Code, rec.Body.String(), response.ErrToolNotInstalled)
	}
	assertNotRun(t, cmd, "parted")
}

// A partition that was made but not formatted is reported as such, so the
// page says so and lists it, rather than calling the whole request failed.
func TestCreatePartitionReportsAPartitionItCouldNotFormat(t *testing.T) {
	for _, tc := range []struct {
		name          string
		after         []int
		mkfsErr       error
		mkfsShouldRun bool
	}{
		{"mkfs fails", []int{1, 2}, errors.New("exit status 1"), true},
		{"no new partition to be found", []int{1}, nil, false},
	} {
		stubPartitionNumbers(t, []int{1}, tc.after)
		cmd := toolsAvailable("parted", "udevadm", "mkfs.ext4")
		cmd.Outputs["mkfs.ext4"] = exec.MockResult{Output: "mke2fs: Device size reported to be zero.", Err: tc.mkfsErr}
		rec := createPartition(t, cmd, "vdb", `{"start":"50%","end":"100%","fs_type":"ext4","format":true}`)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), response.ErrPartitionNotFormatted) {
			t.Errorf("%s: got %d %s, want 500 %s", tc.name, rec.Code, rec.Body.String(), response.ErrPartitionNotFormatted)
		}
		if ran := findCall(cmd.Calls, "mkfs.ext4") != nil; ran != tc.mkfsShouldRun {
			t.Errorf("%s: mkfs ran = %v, want %v", tc.name, ran, tc.mkfsShouldRun)
		}
	}
}
