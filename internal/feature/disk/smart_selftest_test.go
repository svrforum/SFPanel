package disk

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// The banner every smartctl run opens with.
const smartctlBanner = "smartctl 7.4 2023-08-01 r5530 [x86_64-linux-6.8.0-146-generic] (local build)\n" +
	"Copyright (C) 2002-23, Bruce Allen, Christian Franke, www.smartmontools.org\n\n"

func runSmartTest(t *testing.T, out string, runErr error) (int, map[string]interface{}) {
	t.Helper()
	m := exec.NewMockCommander()
	m.SetOutput("exists:smartctl", "", nil)
	m.SetOutput("smartctl", out, runErr)
	h := &Handler{Cmd: m}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/disks/sda/smart/test", strings.NewReader(`{"type":"short"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("device")
	c.SetParamValues("sda")
	if err := h.RunSmartTest(c); err != nil {
		t.Fatalf("RunSmartTest: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func errorCode(body map[string]interface{}) string {
	e, _ := body["error"].(map[string]interface{})
	code, _ := e["code"].(string)
	return code
}

// A test already running is not a start: it was shown as a green "started"
// notice holding smartctl's banner (reported on the forum, from a SCSI-class
// drive). Every transport's wording is a 409 with its own code.
func TestRunSmartTest_RefusedWhileATestRuns(t *testing.T) {
	for name, out := range map[string]string{
		"scsi": smartctlBanner + "Can't start self-test without aborting current test (74% remaining),\nadd '-t force' option to override, or run 'smartctl -X' to abort test.\n",
		"ata":  smartctlBanner + "=== START OF OFFLINE IMMEDIATE AND SELF-TEST SECTION ===\nCan't start self-test without aborting current test (70% remaining),\nadd '-t force' option to override, or run 'smartctl -X' to abort test.\n",
		"nvme": smartctlBanner + "Can't start self-test without aborting current test (30% completed)\nUse smartctl -X to abort test\n",
	} {
		code, body := runSmartTest(t, out, errors.New("exit status 4"))
		if code != http.StatusConflict || errorCode(body) != "SMART_TEST_IN_PROGRESS" {
			t.Errorf("%s: %d %v, want 409 SMART_TEST_IN_PROGRESS", name, code, body)
		}
		if msg, _ := body["error"].(map[string]interface{})["message"].(string); strings.Contains(msg, "Copyright") {
			t.Errorf("%s: the banner reached the operator: %q", name, msg)
		}
	}
}

// smartctl's exit code reports the disk's logs too: a drive with old errors
// in its error log (bit 6) exits 64 having started the test fine.
func TestRunSmartTest_StartedDespiteANonZeroExit(t *testing.T) {
	out := smartctlBanner + "=== START OF OFFLINE IMMEDIATE AND SELF-TEST SECTION ===\n" +
		"Sending command: \"Execute SMART Short self-test routine immediately in off-line mode\".\n" +
		"Drive command \"Execute SMART Short self-test routine immediately in off-line mode\" successful.\n" +
		"Testing has begun.\nPlease wait 2 minutes for test to complete.\nTest will complete after Sun Oct 11 01:02:03 2026 KST\nUse smartctl -X to abort test.\n"
	code, body := runSmartTest(t, out, errors.New("exit status 64"))
	if code != http.StatusOK {
		t.Fatalf("%d %v, want 200", code, body)
	}
	data, _ := body["data"].(map[string]interface{})
	if data["minutes"] != float64(2) {
		t.Errorf("minutes = %v, want 2", data["minutes"])
	}
	if output, _ := data["output"].(string); strings.Contains(output, "Copyright") || strings.Contains(output, "===") || !strings.Contains(output, "Testing has begun.") {
		t.Errorf("output = %q, want the run's own lines only", output)
	}

	code, body = runSmartTest(t, smartctlBanner+"Self-test has begun (NSID 0x1)\nUse smartctl -X to abort test\n", nil)
	if code != http.StatusOK {
		t.Fatalf("nvme start: %d %v, want 200", code, body)
	}
}

func TestRunSmartTest_FailureIsAnError(t *testing.T) {
	code, body := runSmartTest(t, smartctlBanner+"Smartctl open device: /dev/sda failed: No such device\n", errors.New("exit status 2"))
	if code != http.StatusInternalServerError || errorCode(body) != "SMART_ERROR" {
		t.Fatalf("%d %v, want 500 SMART_ERROR", code, body)
	}
	msg, _ := body["error"].(map[string]interface{})["message"].(string)
	if !strings.Contains(msg, "No such device") || strings.Contains(msg, "Copyright") {
		t.Errorf("message = %q", msg)
	}
}

func TestSelfTestProgress(t *testing.T) {
	cases := map[string]struct {
		json      string
		running   bool
		remaining int
	}{
		"ata running":  {`{"ata_smart_data":{"self_test":{"status":{"value":247,"string":"in progress, 70% remaining","remaining_percent":70}}}}`, true, 70},
		"ata idle":     {`{"ata_smart_data":{"self_test":{"status":{"value":0,"string":"completed without error","passed":true},"polling_minutes":{"short":2,"extended":10}}}}`, false, 0},
		"nvme running": {`{"nvme_self_test_log":{"current_self_test_operation":{"value":1,"string":"Short self-test in progress"},"current_self_test_completion_percent":30}}`, true, 70},
		"nvme idle":    {`{"nvme_self_test_log":{"current_self_test_operation":{"value":0,"string":"No self-test in progress"}}}`, false, 0},
		"scsi running": {`{"scsi_self_test_0":{"code":{"value":1},"self_test_in_progress":true}}`, true, 0},
		"scsi idle":    {`{"scsi_self_test_0":{"code":{"value":1},"power_on_time":{"hours":12}}}`, false, 0},
	}
	for name, tc := range cases {
		info, err := parseSmartctlJSON("/dev/sda", []byte(tc.json))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.SelfTestRunning != tc.running || info.SelfTestRemaining != tc.remaining {
			t.Errorf("%s: running=%v remaining=%d, want %v %d", name, info.SelfTestRunning, info.SelfTestRemaining, tc.running, tc.remaining)
		}
	}
}
