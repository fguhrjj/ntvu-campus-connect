package power

import (
	"context"
	"testing"
)

func TestResumeCallbackCanUnsubscribe(t *testing.T) {
	var resumes int
	var id uintptr
	released := 0
	unsubscribe, err := subscribe(context.Background(), func() { resumes++ }, func(i uintptr) (func(), error) {
		id = i
		return func() { released++ }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handleEvent(id, pbtAPMSuspend)
	handleEvent(id, pbtAPMResumeAutomatic)
	handleEvent(id, pbtAPMResumeSuspend)
	if resumes != 2 {
		t.Fatalf("resumes = %d, want 2 (suspend ignored)", resumes)
	}
	unsubscribe()
	unsubscribe()
	handleEvent(id, pbtAPMResumeAutomatic)
	if resumes != 2 {
		t.Fatalf("resume delivered after unsubscribe: %d", resumes)
	}
	if released != 1 {
		t.Fatalf("OS registration released %d times, want 1", released)
	}
}
