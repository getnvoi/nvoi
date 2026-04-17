package kube

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// LogsRequest mirrors kubectl-logs flags relevant to nvoi.
type LogsRequest struct {
	Namespace  string
	Selector   string // typically PodSelector(service)
	Follow     bool
	Tail       int
	Since      string // duration string ("5m"), parsed by client-go
	Previous   bool
	Timestamps bool
}

// StreamLogs streams logs for every pod matching opts.Selector to w.
// When Follow is true, the call blocks until ctx is cancelled or every pod
// stream ends.
func (c *Client) StreamLogs(ctx context.Context, w io.Writer, opts LogsRequest) error {
	pods, err := c.cs.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: opts.Selector,
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods match %s", opts.Selector)
	}

	logOpts := &corev1.PodLogOptions{
		Follow:     opts.Follow,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	}
	if opts.Tail > 0 {
		logOpts.TailLines = int64Ptr(int64(opts.Tail))
	}
	if opts.Since != "" {
		dur, err := time.ParseDuration(opts.Since)
		if err != nil {
			return fmt.Errorf("parse --since=%q: %w", opts.Since, err)
		}
		secs := int64(dur.Seconds())
		logOpts.SinceSeconds = &secs
	}

	// Single pod: stream directly without prefix.
	if len(pods.Items) == 1 {
		return c.streamOne(ctx, w, opts.Namespace, pods.Items[0].Name, logOpts)
	}

	// Multiple pods: stream each into w, prefixed with the pod name.
	// Sequential — preserves order within a pod, simple to reason about.
	// (Follow mode across multiple pods stays sequential too — first pod's
	// stream blocks the rest. Acceptable for nvoi's typical 2-replica case;
	// if needed later, parallelize with goroutines and a mutex on w.)
	for _, pod := range pods.Items {
		prefixed := &prefixedWriter{w: w, prefix: []byte("[" + pod.Name + "] ")}
		if err := c.streamOne(ctx, prefixed, opts.Namespace, pod.Name, logOpts); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) streamOne(ctx context.Context, w io.Writer, ns, pod string, opts *corev1.PodLogOptions) error {
	stream, err := c.cs.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream logs %s/%s: %w", ns, pod, err)
	}
	defer stream.Close()
	_, err = io.Copy(w, stream)
	return err
}

// ExecRequest is the input to Exec.
type ExecRequest struct {
	Namespace string
	Pod       string
	Container string // empty = first container
	Command   []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	TTY       bool
}

// Exec runs a command in a pod via the apiserver's exec subresource.
// Streams over the same SSH tunnel — no shell quoting, no kubectl wrapper.
func (c *Client) Exec(ctx context.Context, req ExecRequest) error {
	if c.cfg == nil {
		return fmt.Errorf("kube client missing rest.Config — Exec requires real apiserver connection")
	}
	restReq := c.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(req.Namespace).
		Name(req.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: req.Container,
			Command:   req.Command,
			Stdin:     req.Stdin != nil,
			Stdout:    req.Stdout != nil,
			Stderr:    req.Stderr != nil,
			TTY:       req.TTY,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.cfg, "POST", restReq.URL())
	if err != nil {
		return fmt.Errorf("build exec: %w", err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  req.Stdin,
		Stdout: req.Stdout,
		Stderr: req.Stderr,
		Tty:    req.TTY,
	})
}

// prefixedWriter prepends `prefix` to each newline-separated chunk.
type prefixedWriter struct {
	w      io.Writer
	prefix []byte
	pend   bool
}

func (p *prefixedWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		if !p.pend {
			if _, err := p.w.Write(p.prefix); err != nil {
				return written, err
			}
			p.pend = true
		}
		nl := -1
		for i, c := range b {
			if c == '\n' {
				nl = i
				break
			}
		}
		if nl < 0 {
			n, err := p.w.Write(b)
			return written + n, err
		}
		n, err := p.w.Write(b[:nl+1])
		written += n
		if err != nil {
			return written, err
		}
		b = b[nl+1:]
		p.pend = false
	}
	return written, nil
}
