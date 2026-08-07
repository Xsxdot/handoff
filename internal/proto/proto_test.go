package proto

import "testing"

func TestCanTransit(t *testing.T) {
	cases := []struct {
		name string
		from TaskState
		to   TaskState
		want bool
	}{
		{"pending_to_running", TaskStatePending, TaskStateRunning, true},
		{"running_to_waiting_answer", TaskStateRunning, TaskStateWaitingAnswer, true},
		{"waiting_answer_to_running", TaskStateWaitingAnswer, TaskStateRunning, true},
		{"running_to_waiting_review", TaskStateRunning, TaskStateWaitingReview, true},
		{"waiting_review_to_running_continue", TaskStateWaitingReview, TaskStateRunning, true},
		{"completed_to_running", TaskStateCompleted, TaskStateRunning, false},
		{"failed_to_running_retry", TaskStateFailed, TaskStateRunning, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanTransit(c.from, c.to); got != c.want {
				t.Errorf("CanTransit(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
			}
		})
	}
}
