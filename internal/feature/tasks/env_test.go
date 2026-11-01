package featuretasks

import (
	"errors"
	"net/http"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func TestANodesRefusalIsReadWithItsCodeAndDetails(t *testing.T) {
	body := `{"success":false,"error":{"code":"TASK_NODE_BUSY","message":"This node is running another task job","details":{"run_id":"r1"}}}`
	err := decodeEnvelope(http.StatusConflict, []byte(body), nil)
	var ref *jobRefusal
	if !errors.As(err, &ref) || ref.Status != http.StatusConflict || ref.Code != response.ErrTaskNodeBusy || ref.Details["run_id"] != "r1" {
		t.Fatalf("err = %#v, want the node's TASK_NODE_BUSY refusal", err)
	}
	if refusalCode(err) != response.ErrTaskNodeBusy {
		t.Errorf("refusalCode = %q", refusalCode(err))
	}
}

func TestANodesAnswerIsReadIntoItsData(t *testing.T) {
	var v tasks.JobView
	if err := decodeEnvelope(http.StatusCreated, []byte(`{"success":true,"data":{"id":"j1","status":"running"}}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.ID != "j1" || v.Status != tasks.JobRunning {
		t.Errorf("view = %+v", v)
	}
}

func TestAnAnswerThatIsNotTheEnvelopeIsNoRefusal(t *testing.T) {
	for _, body := range []string{"<html>bad gateway</html>", `{"success":false}`} {
		err := decodeEnvelope(http.StatusBadGateway, []byte(body), nil)
		var ref *jobRefusal
		if err == nil || errors.As(err, &ref) {
			t.Errorf("%s: err = %#v, want an error that is not a node's refusal", body, err)
		}
	}
}
