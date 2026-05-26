package terminal

import (
	"bytes"
	"context"
	"strings"
	"testing"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coretask "github.com/fluxplane/fluxplane-core/core/task"
	"github.com/fluxplane/fluxplane-core/core/testrun"
	"github.com/fluxplane/fluxplane-core/core/usage"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionagent"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

func TestRendererRendersFocusAndSurfaceEvents(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	events := []clientapi.Event{
		{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: coreactivation.EventFocusDetected,
				Payload: coreactivation.FocusDetected{
					Objective:  "Troubleshoot backend load",
					Intents:    []string{"troubleshoot"},
					Subjects:   []coreevidence.Subject{{Name: "slack"}, {Name: "backend"}},
					Source:     coreactivation.SourceModelFocus,
					Confidence: 0.86,
				},
			},
		},
		{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: coreactivation.EventSurfacePrepareRequested,
				Payload: map[string]any{
					"terms":    []any{"slack", "jira"},
					"lifetime": "run",
					"source":   "user_command",
				},
			},
		},
		{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: coreactivation.EventSurfacePrepared,
				Payload: coreactivation.SurfacePrepared{
					ActivationSets:   []string{"incident.slack_jira"},
					Operations:       []operation.Ref{{Name: "slack_thread_read"}},
					OperationSets:    []string{"jira.issue_read"},
					ContextProviders: []corecontext.ProviderRef{{Name: "surface.schema"}},
					Datasources:      []coredatasource.Ref{{Name: "jira"}},
					Lifetime:         coreactivation.LifetimeRun,
					Source:           coreactivation.SourceReaction,
				},
			},
		},
	}
	for _, event := range events {
		renderer.Render(event)
	}

	got := err.String()
	for _, want := range []string{
		"focus:",
		"Troubleshoot backend load",
		"source=model_focus",
		"confidence=0.86",
		"surface requested:",
		"duration=run",
		"source=user_command",
		"surface prepared:",
		"incident.slack_jira",
		"operations: slack_thread_read",
		"operation sets: jira.issue_read",
		"context: surface.schema",
		"datasources: jira",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output = %q, missing %q", got, want)
		}
	}
}

func TestRendererStreamsMarkdownContent(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "**hello** `world`",
			}},
		},
	})
	renderer.Finish()
	if !renderer.HasStreamedContent() {
		t.Fatalf("HasStreamedContent = false, want true")
	}
	if !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), "world") {
		t.Fatalf("out = %q", out.String())
	}
	if strings.Contains(out.String(), "**hello**") || strings.Contains(out.String(), "`world`") {
		t.Fatalf("out = %q, want rendered markdown without source markers", out.String())
	}
}

func TestApproverAcceptsYes(t *testing.T) {
	var out bytes.Buffer
	approver := Approver{In: strings.NewReader("y\n"), Out: &out}

	err := approver.Approve(operation.NewContext(context.Background(), nil), operationruntime.ApprovalRequest{
		Spec:  operation.Spec{Ref: operation.Ref{Name: "git_commit"}},
		Input: map[string]any{"message": "docs"},
		Risk:  operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true},
	})

	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	text := out.String()
	for _, want := range []string{"approval required: git_commit", "risk: high", "reason: needs review", `"message":"docs"`, "Approve? [y/N]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval prompt missing %q:\n%s", want, text)
		}
	}
}

func TestApproverDeniesNoAndEmptyInput(t *testing.T) {
	for _, input := range []string{"n\n", "\n"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			approver := Approver{In: strings.NewReader(input), Out: &bytes.Buffer{}}

			err := approver.Approve(operation.NewContext(context.Background(), nil), operationruntime.ApprovalRequest{
				Spec: operation.Spec{Ref: operation.Ref{Name: "git_commit"}},
				Risk: operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true},
			})

			if err == nil {
				t.Fatal("Approve error is nil, want denial")
			}
		})
	}
}

func TestRendererRendersApprovalEvents(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: operationruntime.EventApprovalDeniedName,
			Payload: operationruntime.ApprovalDenied{
				Resource:  policy.ResourceRef{Kind: policy.ResourceOperation, Name: "shell_exec"},
				Action:    policy.ActionApprovalGrant,
				Operation: operation.Ref{Name: "shell_exec"},
				Risk:      operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "needs review"},
				Error:     "approval_unauthorized: no_grants",
			},
		},
	})

	got := err.String()
	for _, want := range []string{"approval denied:", "shell_exec", "resource=operation:shell_exec", "action=approval.grant", "risk=high", "reason=needs review", "approval_unauthorized"} {
		if !strings.Contains(got, want) {
			t.Fatalf("approval output = %q, missing %q", got, want)
		}
	}
}

func TestRendererStreamsAllContentDeltas(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"Hi", ", I", " can", " help."} {
		renderer.Render(clientapi.Event{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: llmagent.EventModelStreamedName,
				Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
					Kind: llmagent.StreamContentDelta,
					Text: text,
				}},
			},
		})
	}
	renderer.Finish()
	got := out.String()
	for _, want := range []string{"Hi", ", I", " can", " help."} {
		if !strings.Contains(got, want) {
			t.Fatalf("out = %q, want streamed delta %q", got, want)
		}
	}
}

func TestRendererFlushesMarkdownListWithoutTrailingNewline(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "- **two**",
			}},
		},
	})
	renderer.Finish()
	got := out.String()
	if !strings.Contains(got, "two") {
		t.Fatalf("out = %q, want rendered list text", got)
	}
	if strings.Contains(got, "**two**") {
		t.Fatalf("out = %q, want rendered markdown without bold markers", got)
	}
}

func TestRendererStreamsBlockMarkdown(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"## README", " summary\n\n- **item**"} {
		renderer.Render(clientapi.Event{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: llmagent.EventModelStreamedName,
				Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
					Kind: llmagent.StreamContentDelta,
					Text: text,
				}},
			},
		})
	}
	renderer.Finish()
	got := out.String()
	if !strings.Contains(got, "README summary") || !strings.Contains(got, "item") {
		t.Fatalf("out = %q, want rendered heading and list", got)
	}
	if strings.Contains(got, "## README") || strings.Contains(got, "**item**") {
		t.Fatalf("out = %q, want rendered markdown without source markers", got)
	}
}

func TestRendererDoesNotReplayContentDeltas(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	for _, text := range []string{"hello", " world"} {
		renderer.Render(clientapi.Event{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name: llmagent.EventModelStreamedName,
				Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
					Kind: llmagent.StreamContentDelta,
					Text: text,
				}},
			},
		})
	}
	renderer.Finish()
	if count := strings.Count(out.String(), "hello"); count != 1 {
		t.Fatalf("out = %q, hello count = %d, want 1", out.String(), count)
	}
}

func TestRendererHidesThinkingDeltasByDefault(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "**checking** `state`",
			}},
		},
	})
	renderer.Finish()
	if got := out.String() + err.String(); got != "" {
		t.Fatalf("thinking output = %q, want empty", got)
	}
}

func TestRendererShowsThinkingDeltasWhenEnabled(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Reasoning = ReasoningDisplayOn
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "checking state",
			}},
		},
	})
	renderer.Finish()
	got := err.String()
	if !strings.Contains(got, "reasoning") || !strings.Contains(got, "checking state") {
		t.Fatalf("thinking output = %q, want reasoning block", got)
	}
	if strings.Contains(got, "raw reasoning") {
		t.Fatalf("thinking output = %q, want non-raw label", got)
	}
}

func TestRendererShowsRawThinkingLabel(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Reasoning = ReasoningDisplayRaw
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "internal trace",
			}},
		},
	})
	renderer.Finish()
	got := err.String()
	if !strings.Contains(got, "raw reasoning") || !strings.Contains(got, "internal trace") {
		t.Fatalf("thinking output = %q, want raw reasoning block", got)
	}
}

func TestRendererIgnoresUntypedRuntimeStreamPayload(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: map[string]any{
				"event": map[string]any{
					"kind": string(llmagent.StreamContentDelta),
					"text": "hello",
				},
			},
		},
	})
	renderer.Finish()
	if got := out.String() + err.String(); got != "" {
		t.Fatalf("untyped runtime output = %q, want empty", got)
	}
}

func TestRendererRendersToolTimeline(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "image_generate"},
			Input:     map[string]any{"prompt": "minimal fluxplane logo", "size": "1024x1024"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "image_generate"},
			Result:    &operation.Result{Status: operation.StatusOK, Output: operation.Rendered{Text: "created image"}},
		},
	})

	got := err.String()
	for _, want := range []string{"●", "image_generate", `  ↳ prompt="minimal fluxplane logo" size=1024x1024`, "✓", "created image"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool block = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "tool start:") || strings.Contains(got, "tool end:") || strings.Contains(got, `{"prompt"`) || strings.Contains(got, "  >") {
		t.Fatalf("tool block = %q, did not want old start/end wording", got)
	}
}

func TestRendererRendersOperationDiffDetail(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "file_edit"},
			Result: &operation.Result{Status: operation.StatusOK, Output: operation.Rendered{
				Text: "Edited note.txt",
				Data: map[string]any{
					"path": "note.txt",
					"diff": "--- note.txt\n+++ note.txt\n@@ -1 +1 @@\n-old\n+new\n",
				},
			}},
		},
	})

	got := err.String()
	for _, want := range []string{"✓", "Edited note.txt", "--- note.txt", "+++ note.txt", "-old", "+new"} {
		if !strings.Contains(got, want) {
			t.Fatalf("operation output = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersTestRunEvent(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "go_test"},
			Result: &operation.Result{Status: operation.StatusOK, Output: operation.Rendered{
				Text: "go_test: PASS ./core/testrun summary: packages=1 passed=1 failed=0 skipped=0; tests=1 passed=1 failed=0 skipped=0",
				Data: map[string]any{
					"test": map[string]any{
						"test_run_event": testrun.Event{Target: "./core/testrun", Status: testrun.StatusPassed, Summary: testrun.Summary{TestsPassed: 1, PackagesPassed: 1}, DurationMS: 102},
					},
				},
			}},
		},
	})

	got := err.String()
	for _, want := range []string{"✓", "✅ tests passed  ./core/testrun", "1 passed", "duration="} {
		if !strings.Contains(got, want) {
			t.Fatalf("test run output = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "go_test: PASS") {
		t.Fatalf("test run output = %q, want event renderer instead of raw operation text", got)
	}
}

func TestRendererRendersFailedTestRunEvent(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "go_test"},
			Result: &operation.Result{Status: operation.StatusOK, Output: operation.Rendered{
				Text: "go_test: FAIL ./runtime/thread",
				Data: map[string]any{
					"test": map[string]any{
						"test_run_event": testrun.Event{Target: "./runtime/thread", Status: testrun.StatusFailed, Summary: testrun.Summary{TestsPassed: 5, TestsFailed: 1}, Failures: []testrun.Failure{{Kind: testrun.FailureAssertion, Test: "TestRetry", File: "runtime/thread/store_test.go", Line: 262, Message: "Append returned error"}}},
					},
				},
			}},
		},
	})

	got := err.String()
	for _, want := range []string{"✓", "❌ tests failed  ./runtime/thread", "TestRetry", "store_test.go:262 Append returned error", "5 passed · 1 failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("test run output = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersToolTimelineFailure(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationRequested,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "git_commit"},
			Input:     map[string]any{"message": "fix timeline UI"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "git_commit"},
			Result: &operation.Result{
				Status: operation.StatusRejected,
				Error:  &operation.Error{Code: "approval_required", Message: "approval required"},
			},
		},
	})

	got := err.String()
	for _, want := range []string{"●", "git_commit", `  ↳ message="fix timeline UI"`, "✕", "rejected", `reason="approval required"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool block = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersTaskLifecycleWithStatusMarkers(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventCreatedName,
			Payload: coretask.Created{
				TaskID: "task_1",
				Task: coretask.Task{
					ID:     "task_1",
					Title:  "Review core package",
					Status: coretask.StatusReady,
					Steps: []coretask.Step{
						{ID: "one", Title: "Inspect package", Profile: "explorer"},
						{ID: "two", Title: "Write report", Profile: "reviewer"},
					},
				},
			},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionStartedName,
			Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepDispatchedName,
			Payload: coretask.StepDispatched{TaskID: "task_1", ExecutionID: "exec_1", StepID: "one", Profile: "explorer"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepProgressedName,
			Payload: coretask.StepProgressed{TaskID: "task_1", ExecutionID: "exec_1", StepID: "one", Message: "read files"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepCompletedName,
			Payload: coretask.StepCompleted{TaskID: "task_1", ExecutionID: "exec_1", StepID: "one"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventArtifactAddedName,
			Payload: coretask.ArtifactAdded{TaskID: "task_1", Artifact: coretask.ArtifactSpec{ID: "report", Kind: coretask.ArtifactReport, Required: true}},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventSchedulerDiagnosticName,
			Payload: coretask.SchedulerDiagnostic{
				TaskID:      "task_1",
				ExecutionID: "exec_1",
				Diagnostic:  coretask.Diagnostic{Code: "task_finalizing_outputs", Message: "all steps are terminal; producing 1 missing required task output(s)"},
			},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionCompletedName,
			Payload: coretask.ExecutionCompleted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventSchedulerDiagnosticName,
			Payload: coretask.SchedulerDiagnostic{
				TaskID:      "task_1",
				ExecutionID: "exec_1",
				StepID:      "one",
				Diagnostic:  coretask.Diagnostic{Code: "task_stale_step_result_ignored", Message: "stale worker result ignored"},
			},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	for _, want := range []string{
		"Review core package",
		"[ready, 2 steps]",
		"task execution:",
		ansiCyan + "●" + ansiReset + " Inspect package",
		"task progress:",
		"read files",
		ansiGreen + "●" + ansiReset + " Inspect package",
		"task artifact:",
		"report [report] required",
		"task finalizing:",
		"[running, finalizing, 2 steps]",
		"task completed:",
		"task scheduler:",
		"stale worker result ignored",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("task output = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersBlockedTaskReasonInSnapshot(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventCreatedName,
			Payload: coretask.Created{
				TaskID: "task_1",
				Task:   coretask.Task{ID: "task_1", Title: "Backend check", Status: coretask.StatusRunning},
			},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventExecutionInterruptedName,
			Payload: coretask.ExecutionInterrupted{
				TaskID:      "task_1",
				ExecutionID: "exec_1",
				Reason:      "task completion blocked: missing required outputs backend-check-summary",
			},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	for _, want := range []string{
		"task blocked:",
		"Backend check",
		"[blocked]",
		"missing required outputs backend-check-summary",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("blocked task output = %q, missing %q", got, want)
		}
	}
}

func TestRendererFiltersNoisyTaskProgressAndIgnoresUntypedTaskPayload(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepProgressedName,
			Payload: coretask.StepProgressed{TaskID: "task_1", StepID: "one", Message: "llmagent.model_streamed"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepProgressedName,
			Payload: coretask.StepProgressed{TaskID: "task_1", StepID: "one", Message: "completed file_read"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepProgressedName,
			Payload: map[string]any{"task_id": "task_1", "step_id": "two", "message": "ignored"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	if strings.Contains(got, "llmagent.model_streamed") || strings.Contains(got, "ignored") {
		t.Fatalf("task progress = %q, want noisy/untyped progress filtered", got)
	}
	if !strings.Contains(got, "completed file_read") {
		t.Fatalf("task progress = %q, want useful task progress retained", got)
	}
}

func TestRendererRendersSessionAgentEvents(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionagent.EventStarted,
			Payload: sessionagent.Started{Causation: sessionagent.Causation{ID: "plan_1:step_1", Profile: coresession.Ref{Name: "worker"}}},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionagent.EventCompleted,
			Payload: sessionagent.Completed{Causation: sessionagent.Causation{ID: "manual_1"}, Output: "done"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	for _, want := range []string{"session agent start:", "plan_1:step_1", "session agent done:", "manual_1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("session-agent output = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersSessionRunEvents(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	cause := sessionrun.Causation{
		ID:            "run-loop:loop:1",
		ChildThreadID: "thread-child",
		Profile:       coresession.Ref{Name: "assistant"},
		Metadata:      map[string]string{"loop_iteration": "1", "loop_count": "3"},
	}
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionrun.EventStarted,
			Payload: sessionrun.Started{Causation: cause, Input: "check temperature in Berlin"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionrun.EventProgressed,
			Payload: sessionrun.Progressed{Causation: cause, Message: "reaction.action_applied"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionrun.EventProgressed,
			Payload: sessionrun.Progressed{Causation: cause, Message: "calling web_search"},
		},
	})
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    sessionrun.EventCompleted,
			Payload: sessionrun.Completed{Causation: cause, Output: "Berlin is 26 C and sunny"},
		},
	})
	renderer.Finish()

	got := out.String() + err.String()
	for _, want := range []string{"loop 1/3 start:", "check temperature in Berlin", "loop 1/3 progress:", "calling web_search", "loop 1/3 done:", "Berlin is 26 C", "thread=thread-child"} {
		if !strings.Contains(got, want) {
			t.Fatalf("session-run output = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "reaction.action_applied") {
		t.Fatalf("session-run output = %q, want noisy runtime progress filtered", got)
	}
}

func TestRendererRendersDebugAsMarkdownFence(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.RenderDebug(clientapi.Event{Kind: clientapi.EventRunCompleted})
	if got := err.String(); !strings.Contains(got, "run.completed") {
		t.Fatalf("debug output = %q, want event JSON", got)
	}
}

func TestRendererDebugRedactsThinkingText(t *testing.T) {
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.RenderDebug(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "secret chain of thought",
			}},
		},
	})
	got := err.String()
	if strings.Contains(got, "secret") || strings.Contains(got, "chain") {
		t.Fatalf("debug output leaked thinking text: %q", got)
	}
	if !strings.Contains(got, "thinking_delta") || !strings.Contains(got, "redaction") {
		t.Fatalf("debug output = %q, want redacted thinking metadata", got)
	}
}

func TestRenderMarkdownRendersMarkdown(t *testing.T) {
	var out bytes.Buffer
	if err := RenderMarkdown(&out, "**hello** `world`"); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("out = %q, want rendered markdown", got)
	}
	if strings.Contains(got, "**hello**") || strings.Contains(got, "`world`") {
		t.Fatalf("out = %q, want rendered markdown without source markers", got)
	}
}

func TestRenderUsageSnapshotGroupsHumanReadableTotals(t *testing.T) {
	var out bytes.Buffer
	tracker := usage.NewTracker()
	tracker.Add(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test", ID: "resp_1"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 2109, Unit: usage.UnitToken, Direction: usage.DirectionInput},
			{Metric: usage.MetricLLMCacheWriteTokens, Quantity: 128, Unit: usage.UnitToken, Direction: usage.DirectionWrite},
			{Metric: usage.MetricLLMCachedTokens, Quantity: 1536, Unit: usage.UnitToken, Direction: usage.DirectionCached},
			{Metric: usage.MetricLLMOutputTokens, Quantity: 11, Unit: usage.UnitToken, Direction: usage.DirectionOutput},
			{Metric: usage.MetricCost, Quantity: 0.0031, Unit: usage.UnitCurrency, Dimensions: map[string]string{"currency": "USD", "estimated": "true"}},
		},
	})
	tracker.Add(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectNetwork, Provider: "codex", Name: "gpt-test"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricNetworkBytes, Quantity: 18628, Unit: usage.UnitByte, Direction: usage.DirectionUpload},
			{Metric: usage.MetricNetworkBytes, Quantity: 61881, Unit: usage.UnitByte, Direction: usage.DirectionDownload},
		},
	})

	RenderUsageSnapshot(&out, tracker.Snapshot())
	got := out.String()
	for _, want := range []string{
		"Total usage",
		"openai/gpt-test",
		"input tokens 2,109",
		"cache write tokens 128",
		"cached input tokens 1,536 | cached 41%",
		"output tokens 11",
		"estimated cost $0.0031",
		"codex/gpt-test",
		"uploaded 18.2 KB",
		"downloaded 60.4 KB",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage output = %q, want %q", got, want)
		}
	}
}

func TestRenderUsageSnapshotOmitsCacheRateWithoutCachedTokens(t *testing.T) {
	var out bytes.Buffer
	snapshot := usage.NewSnapshot(usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 100, Unit: usage.UnitToken, Direction: usage.DirectionInput},
		},
	})

	RenderUsageSnapshot(&out, snapshot)
	if strings.Contains(out.String(), "| cached") {
		t.Fatalf("usage output = %q, did not want cache rate", out.String())
	}
}

func TestRenderUsageSnapshotEmpty(t *testing.T) {
	var out bytes.Buffer
	RenderUsageSnapshot(&out, usage.Snapshot{})
	if out.Len() != 0 {
		t.Fatalf("usage output = %q, want empty", out.String())
	}
}

func TestRenderUsageRequestPrintsCompactContextLine(t *testing.T) {
	recorded := usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "anthropic", Name: "claude-sonnet"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 45231, Unit: usage.UnitToken},
			{Metric: usage.MetricLLMCachedTokens, Quantity: 12000, Unit: usage.UnitToken},
			{Metric: usage.MetricLLMCacheWriteTokens, Quantity: 512, Unit: usage.UnitToken},
			{Metric: usage.MetricLLMOutputTokens, Quantity: 312, Unit: usage.UnitToken},
		},
	}
	var out bytes.Buffer
	RenderUsageRequest(&out, recorded)
	got := out.String()
	for _, want := range []string{"anthropic/claude-sonnet", "ctx 57,743", "cached 12,000", "cache write 512", "out 312"} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderUsageRequest output = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "\n") && strings.Count(got, "\n") != 1 {
		t.Fatalf("RenderUsageRequest output = %q, want exactly one line", got)
	}
	// Must not include the verbose "Total usage" heading used by RenderUsageSnapshot.
	if strings.Contains(got, "Total usage") {
		t.Fatalf("RenderUsageRequest output = %q, must not contain 'Total usage'", got)
	}
}

func TestRenderUsageRequestSkipsNonLLMSubjects(t *testing.T) {
	var out bytes.Buffer
	RenderUsageRequest(&out, usage.Recorded{
		Subject:      usage.Subject{Kind: usage.SubjectNetwork},
		Measurements: []usage.Measurement{{Metric: usage.MetricNetworkBytes, Quantity: 1024, Unit: usage.UnitByte}},
	})
	if out.Len() != 0 {
		t.Fatalf("RenderUsageRequest output = %q, want empty for non-LLM subject", out.String())
	}
}

func TestRenderUsageRequestSkipsEmptyMeasurements(t *testing.T) {
	var out bytes.Buffer
	RenderUsageRequest(&out, usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Name: "model"},
	})
	if out.Len() != 0 {
		t.Fatalf("RenderUsageRequest output = %q, want empty for no measurements", out.String())
	}
}

func TestRenderUsageRequestInputOnly(t *testing.T) {
	var out bytes.Buffer
	RenderUsageRequest(&out, usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Name: "model"},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricLLMInputTokens, Quantity: 8192, Unit: usage.UnitToken},
		},
	})
	got := out.String()
	if !strings.Contains(got, "ctx 8,192") {
		t.Fatalf("RenderUsageRequest output = %q, want ctx token count", got)
	}
	if strings.Contains(got, "cached") || strings.Contains(got, "out") {
		t.Fatalf("RenderUsageRequest output = %q, must not include absent fields", got)
	}
}

func TestRendererEmitsPerRequestUsageWhenShowUsageEnabled(t *testing.T) {
	var out, errOut bytes.Buffer
	renderer := NewRenderer(&out, &errOut, true)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: usage.EventRecordedName,
			Payload: usage.Recorded{
				Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
				Measurements: []usage.Measurement{
					{Metric: usage.MetricLLMInputTokens, Quantity: 5000, Unit: usage.UnitToken},
					{Metric: usage.MetricLLMOutputTokens, Quantity: 200, Unit: usage.UnitToken},
				},
			},
		},
	})
	// RenderUsageRequest writes to Err (the renderer's stderr writer).
	got := errOut.String()
	if !strings.Contains(got, "ctx 5,000") {
		t.Fatalf("renderer ShowUsage output = %q, want ctx token line", got)
	}
	if !strings.Contains(got, "out 200") {
		t.Fatalf("renderer ShowUsage output = %q, want output token count", got)
	}
	if strings.Contains(got, "Total usage") {
		t.Fatalf("renderer ShowUsage output = %q, must not print 'Total usage' per-request", got)
	}
}

func TestRendererSuppressesPerRequestUsageWhenShowUsageDisabled(t *testing.T) {
	var out, errOut bytes.Buffer
	renderer := NewRenderer(&out, &errOut, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: usage.EventRecordedName,
			Payload: usage.Recorded{
				Subject: usage.Subject{Kind: usage.SubjectLLM, Name: "model"},
				Measurements: []usage.Measurement{
					{Metric: usage.MetricLLMInputTokens, Quantity: 1000, Unit: usage.UnitToken},
				},
			},
		},
	})
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("renderer output = %q / %q, want no usage output when ShowUsage=false", out.String(), errOut.String())
	}
}
