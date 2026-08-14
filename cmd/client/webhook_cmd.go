// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fortunnels/client/internal/webhookapi"
)

// Exit codes for the webhook CLI family. Stable contract documented in
// docs/api/webhook-test-openapi.yaml and the FKB.
const (
	exitOK    = 0 // match or success
	exitFail  = 1 // assertion or wait timeout
	exitError = 2 // auth/config/transport/API error
)

// envWebhookToken is the documented environment token source.
const envWebhookToken = "FORTUNNELS_API_TOKEN"

// envAPIURL overrides the default API base URL in local/dev stacks.
const envAPIURL = "FORTUNNELS_API_URL"

// maxServerWaitMS is the server-side wait ceiling; longer CLI waits retry.
const maxServerWaitMS = 30000

// runWebhookCommand dispatches the fortunnels webhook subcommands.
func runWebhookCommand(args []string) int {
	if len(args) == 0 {
		printWebhookUsage(os.Stderr)
		return exitError
	}
	switch args[0] {
	case "send":
		return runWebhookSend(args[1:])
	case "events":
		return runWebhookEvents(args[1:])
	case "wait":
		return runWebhookWaitOrAssert(args[1:], false)
	case "assert":
		return runWebhookWaitOrAssert(args[1:], true)
	case "clear":
		return runWebhookClear(args[1:])
	case "mock":
		return runWebhookMock(args[1:])
	case "help", "-h", "--help":
		printWebhookUsage(os.Stdout)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown webhook command: %s\n", args[0])
		printWebhookUsage(os.Stderr)
		return exitError
	}
}

func printWebhookUsage(w *os.File) {
	fmt.Fprintln(w, `usage: fortunnels webhook <command> [flags]

Send test webhook events, wait for and assert on captured events, clear test
data, and manage mock rules via a scoped API token.

Commands:
  send        Send one test event to an endpoint
  events list List captured events for an endpoint
  wait        Wait for a matching event (exit 0 match, 1 timeout)
  assert      Wait for a matching event and assert its status (exit 0/1)
  clear       Delete (or dry-run count) test-generated events
  mock        Manage Stage 7 mock rules (list|create|update|delete)

Token sources: --api-token flag or FORTUNNELS_API_TOKEN. The token is never
printed. Exit codes: 0 success/match, 1 wait/assert timeout, 2 API/transport
error.

Run 'fortunnels webhook <command> --help' for command flags.`)
}

// webhookFlags holds the shared API connection flags.
type webhookFlags struct {
	apiToken string
	apiURL   string
}

func (f *webhookFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.apiToken, "api-token", "", "webhook-test API token (or "+envWebhookToken+")")
	fs.StringVar(&f.apiURL, "api-url", os.Getenv(envAPIURL), "API base URL (default "+webhookapi.DefaultBaseURL+")")
}

func (f *webhookFlags) client() (*webhookapi.Client, error) {
	token := f.apiToken
	if strings.TrimSpace(token) == "" {
		token = os.Getenv(envWebhookToken)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("no API token: pass --api-token or set %s", envWebhookToken)
	}
	return webhookapi.New(f.apiURL, token), nil
}

func webhookErrorExit(what string, err error) int {
	if webhookapi.IsAuth(err) {
		fmt.Fprintf(os.Stderr, "❌ %s: %v\n", what, err)
		return exitError
	}
	fmt.Fprintf(os.Stderr, "❌ %s: %v\n", what, err)
	return exitError
}

// --- send ----------------------------------------------------------------

func runWebhookSend(args []string) int {
	fs := flag.NewFlagSet("webhook send", flag.ExitOnError)
	var (
		common      webhookFlags
		endpoint    = fs.String("endpoint", "", "endpoint id")
		method      = fs.String("method", "POST", "HTTP method")
		path        = fs.String("path", "/", "request path")
		query       = fs.String("query", "", "raw query string")
		contentType = fs.String("content-type", "", "Content-Type header")
		body        = fs.String("body", "", "request body")
		runID       = fs.String("run-id", "", "run id (tags the event webhook-run:<run>)")
		headers     = multiFlag{}
	)
	fs.Var(&headers, "header", "extra header in Name:value form (repeatable)")
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook send --endpoint <id> [flags]")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	headerMap := map[string]string{}
	for _, h := range headers {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			fmt.Fprintf(os.Stderr, "invalid header %q (want Name:value)\n", h)
			return exitError
		}
		headerMap[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	result, err := client.Send(context.Background(), &webhookapi.SendRequest{
		EndpointID: *endpoint,
		Request: webhookapi.SendRequestReq{
			Method: *method, Path: *path, RawQuery: *query,
			ContentType: *contentType, Headers: headerMap, Body: *body,
		},
		RunID: *runID,
	})
	if err != nil {
		return webhookErrorExit("send failed", err)
	}
	fmt.Printf("event_id=%s delivered=%v", result.EventID, result.Delivered)
	if result.DownstreamStatus != nil {
		fmt.Printf(" status=%d", *result.DownstreamStatus)
	}
	if result.Notes != "" {
		fmt.Printf(" notes=%s", result.Notes)
	}
	fmt.Println()
	return exitOK
}

// --- events list ----------------------------------------------------------

func runWebhookEvents(args []string) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook events list --endpoint <id> [--cursor ...] [--limit ...]")
		return exitError
	}
	fs := flag.NewFlagSet("webhook events list", flag.ExitOnError)
	var (
		common webhookFlags
		ep     = fs.String("endpoint", "", "endpoint id")
		cursor = fs.String("cursor", "", "opaque pagination cursor")
		limit  = fs.Int("limit", 50, "max events per page")
	)
	common.register(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return exitError
	}
	if *ep == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook events list --endpoint <id>")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	page, err := client.ListEvents(context.Background(), *ep, *cursor, *limit)
	if err != nil {
		return webhookErrorExit("list failed", err)
	}
	for i := range page.Events {
		ev := page.Events[i]
		fmt.Printf("%s %s %s %d %s\n", ev.Timestamp, ev.Method, ev.Path, ev.Status, ev.ID)
	}
	if page.NextCursor != "" {
		fmt.Printf("next_cursor=%s\n", page.NextCursor)
	}
	fmt.Printf("total=%d\n", len(page.Events))
	return exitOK
}

// --- wait / assert --------------------------------------------------------

func runWebhookWaitOrAssert(args []string, assert bool) int {
	name := "wait"
	if assert {
		name = "assert"
	}
	fs := flag.NewFlagSet("webhook "+name, flag.ExitOnError)
	var (
		common     webhookFlags
		ep         = fs.String("endpoint", "", "endpoint id")
		cursor     = fs.String("cursor", "", "opaque cursor to resume from")
		timeoutMS  = fs.Int("timeout", 30000, "total wait timeout in ms")
		method     = fs.String("method", "", "match method")
		path       = fs.String("path", "", "match path")
		status     = fs.Int("status", 0, "match status code")
		tags       = multiFlag{}
		headers    = multiFlag{}
		bodyPoints = multiFlag{}
		bodyValues = multiFlag{}
		assertStat = fs.Int("assert-status", 0, "assert matched event status (assert only)")
	)
	fs.Var(&tags, "tag", "match tag (repeatable)")
	fs.Var(&headers, "header", "match header in Name:value form (repeatable)")
	fs.Var(&bodyPoints, "body-pointer", "match JSON Pointer in masked body (repeatable)")
	fs.Var(&bodyValues, "body-value", "expected value for --body-pointer (repeatable)")
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" {
		fmt.Fprintf(os.Stderr, "usage: fortunnels webhook %s --endpoint <id> [--timeout <ms>] [matcher flags]\n", name)
		return exitError
	}
	if *timeoutMS <= 0 || *timeoutMS > 30*60*1000 {
		fmt.Fprintf(os.Stderr, "invalid timeout %d ms (1..1800000)\n", *timeoutMS)
		return exitError
	}
	matcher, err := buildWebhookMatcher(*method, *path, *status, tags, headers, bodyPoints, bodyValues)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError
	}

	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}

	deadline := time.Now().Add(time.Duration(*timeoutMS) * time.Millisecond)
	step := *timeoutMS
	if step > maxServerWaitMS {
		step = maxServerWaitMS
	}
	curr := *cursor
	backoff := 200 * time.Millisecond
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Fprintf(os.Stderr, "no matching event within %d ms\n", *timeoutMS)
			return exitFail
		}
		waitMS := int(remaining.Milliseconds())
		if waitMS > step {
			waitMS = step
		}
		resp, err := client.Wait(context.Background(), webhookapi.WaitRequest{
			EndpointID: *ep, Cursor: curr, TimeoutMS: waitMS, Matcher: matcher,
		})
		if err == nil {
			if !resp.Matched || resp.Event == nil {
				fmt.Fprintf(os.Stderr, "no matching event within %d ms\n", *timeoutMS)
				return exitFail
			}
			ev := resp.Event
			if assert && *assertStat != 0 && ev.Status != *assertStat {
				fmt.Fprintf(os.Stderr, "assertion failed: status=%d want=%d event_id=%s\n", ev.Status, *assertStat, ev.ID)
				return exitFail
			}
			fmt.Printf("matched event_id=%s method=%s path=%s status=%d\n", ev.ID, ev.Method, ev.Path, ev.Status)
			if resp.Cursor != "" {
				fmt.Printf("cursor=%s\n", resp.Cursor)
			}
			return exitOK
		}
		if !webhookapi.IsTimeout(err) {
			return webhookErrorExit(name+" failed", err)
		}
		if waitMS >= step {
			// Server-side slice expired; back off briefly and re-query.
			time.Sleep(backoff)
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}

// --- clear ----------------------------------------------------------------

// buildWebhookMatcher assembles the wait/assert matcher payload from CLI flags.
func buildWebhookMatcher(method, path string, status int, tags, headers, bodyPoints, bodyValues multiFlag) (map[string]any, error) {
	matcher := map[string]any{}
	if method != "" {
		matcher["method"] = method
	}
	if path != "" {
		matcher["path"] = path
	}
	if status != 0 {
		matcher["status"] = status
	}
	if len(tags) > 0 {
		matcher["tags"] = tags
	}
	var headerList []map[string]string
	for _, h := range headers {
		key, value, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header matcher %q (want Name:value)", h)
		}
		headerList = append(headerList, map[string]string{"name": strings.TrimSpace(key), "value": strings.TrimSpace(value)})
	}
	if len(headerList) > 0 {
		matcher["headers"] = headerList
	}
	if len(bodyPoints) != len(bodyValues) {
		return nil, fmt.Errorf("--body-pointer and --body-value must be paired")
	}
	var bodyList []map[string]string
	for i, p := range bodyPoints {
		bodyList = append(bodyList, map[string]string{"pointer": p, "value": bodyValues[i]})
	}
	if len(bodyList) > 0 {
		matcher["body"] = bodyList
	}
	return matcher, nil
}

func runWebhookClear(args []string) int {
	fs := flag.NewFlagSet("webhook clear", flag.ExitOnError)
	var (
		common webhookFlags
		ep     = fs.String("endpoint", "", "endpoint id")
		runID  = fs.String("run-id", "", "only clear events for this run id")
		from   = fs.String("from", "", "RFC3339 lower bound")
		to     = fs.String("to", "", "RFC3339 upper bound")
		dryRun = fs.Bool("dry-run", false, "count without deleting")
	)
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook clear --endpoint <id> [--run-id ...] [--dry-run]")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	resp, err := client.Clear(context.Background(), webhookapi.ClearRequest{
		EndpointID: *ep, RunID: *runID, From: *from, To: *to, DryRun: *dryRun,
	})
	if err != nil {
		return webhookErrorExit("clear failed", err)
	}
	if resp.DryRun {
		fmt.Printf("dry-run: %d test events would be cleared\n", resp.Count)
	} else {
		fmt.Printf("cleared %d test events\n", resp.Count)
	}
	return exitOK
}

// --- mock ----------------------------------------------------------------

func runWebhookMock(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook mock <list|create|update|delete> [flags]")
		return exitError
	}
	switch args[0] {
	case "list":
		return runWebhookMockList(args[1:])
	case "create":
		return runWebhookMockCreate(args[1:])
	case "update":
		return runWebhookMockUpdate(args[1:])
	case "delete":
		return runWebhookMockDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mock command: %s\n", args[0])
		return exitError
	}
}

func runWebhookMockList(args []string) int {
	fs := flag.NewFlagSet("webhook mock list", flag.ExitOnError)
	var (
		common webhookFlags
		ep     = fs.String("endpoint", "", "endpoint id")
	)
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook mock list --endpoint <id>")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	rules, err := client.ListMock(context.Background(), *ep)
	if err != nil {
		return webhookErrorExit("mock list failed", err)
	}
	for i := range rules {
		r := rules[i]
		fmt.Printf("%s pri=%d enabled=%v method=%s path=%s status=%d\n",
			r.ID, r.Priority, r.Enabled, r.Method, r.Path, r.Response.Status)
	}
	fmt.Printf("total=%d\n", len(rules))
	return exitOK
}

func runWebhookMockCreate(args []string) int {
	fs := flag.NewFlagSet("webhook mock create", flag.ExitOnError)
	var (
		common  webhookFlags
		ep      = fs.String("endpoint", "", "endpoint id")
		method  = fs.String("method", "GET", "method to match")
		path    = fs.String("path", "/", "path to match")
		status  = fs.Int("status", 200, "response status")
		body    = fs.String("body", "", "response body")
		delayMS = fs.Int("delay-ms", 0, "response delay")
		maxUses = fs.Int("max-uses", 0, "max uses (0 = unlimited)")
	)
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook mock create --endpoint <id> [flags]")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	var maxUsesPtr *int
	if *maxUses > 0 {
		maxUsesPtr = intPtr(*maxUses)
	}
	rule, err := client.CreateMock(context.Background(), *ep, &webhookapi.MockRuleInput{
		Method: *method, Path: *path,
		Response: webhookapi.MockResp{Status: *status, Body: *body, DelayMS: *delayMS},
		MaxUses:  maxUsesPtr,
	})
	if err != nil {
		return webhookErrorExit("mock create failed", err)
	}
	fmt.Printf("created rule=%s\n", rule.ID)
	return exitOK
}

func runWebhookMockUpdate(args []string) int {
	fs := flag.NewFlagSet("webhook mock update", flag.ExitOnError)
	var (
		common  webhookFlags
		ep      = fs.String("endpoint", "", "endpoint id")
		ruleID  = fs.String("rule", "", "rule id")
		method  = fs.String("method", "", "method to match")
		path    = fs.String("path", "", "path to match")
		status  = fs.Int("status", 0, "response status")
		body    = fs.String("body", "", "response body")
		delayMS = fs.Int("delay-ms", -1, "response delay")
		version = fs.Int("version", 0, "expected rule version")
		maxUses = fs.Int("max-uses", 0, "max uses (0 = keep current)")
	)
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" || *ruleID == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook mock update --endpoint <id> --rule <id> [flags]")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	var maxUsesPtr *int
	if *maxUses > 0 {
		maxUsesPtr = intPtr(*maxUses)
	}
	resp := webhookapi.MockResp{Status: *status, Body: *body}
	if *delayMS >= 0 {
		resp.DelayMS = *delayMS
	}
	rule, err := client.UpdateMock(context.Background(), *ep, *ruleID, &webhookapi.MockRuleInput{
		Method: *method, Path: *path, Response: resp, MaxUses: maxUsesPtr, Version: *version,
	})
	if err != nil {
		return webhookErrorExit("mock update failed", err)
	}
	fmt.Printf("updated rule=%s version=%d\n", rule.ID, rule.Version)
	return exitOK
}

func runWebhookMockDelete(args []string) int {
	fs := flag.NewFlagSet("webhook mock delete", flag.ExitOnError)
	var (
		common webhookFlags
		ep     = fs.String("endpoint", "", "endpoint id")
		ruleID = fs.String("rule", "", "rule id")
	)
	common.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *ep == "" || *ruleID == "" {
		fmt.Fprintln(os.Stderr, "usage: fortunnels webhook mock delete --endpoint <id> --rule <id>")
		return exitError
	}
	client, err := common.client()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitError
	}
	if err := client.DeleteMock(context.Background(), *ep, *ruleID); err != nil {
		return webhookErrorExit("mock delete failed", err)
	}
	fmt.Printf("deleted rule=%s\n", *ruleID)
	return exitOK
}

// multiFlag collects repeated string flags.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func intPtr(v int) *int { return &v }
