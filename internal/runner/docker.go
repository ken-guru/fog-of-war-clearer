// Package runner provides a Docker-based sandbox for executing coverage
// analysis tools against a cloned repository.
//
// Security design:
//   - The cloned repository is mounted read-only into the container.
//   - A writable workspace directory is created separately and mounted
//     read-write so that build artifacts can be produced.
//   - The PAT is never passed to the container; it stays on the host.
//   - Containers run as a non-root user where the image supports it.
//   - Each container is removed immediately after it exits.
//   - Resource limits (memory, CPU) are applied.
//   - All containers are force-removed on context cancellation.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	// containerTimeout is the maximum time a container is allowed to run.
	containerTimeout = 20 * time.Minute

	// memoryLimit is the maximum memory a container may use.
	memoryLimit int64 = 2 * 1024 * 1024 * 1024 // 2 GiB

	// cpuPeriod / cpuQuota gives 2 vCPUs.
	cpuPeriod int64 = 100_000
	cpuQuota  int64 = 200_000
)

// Runner executes commands inside Docker containers.
type Runner struct {
	cli *client.Client
}

// New creates a Runner using the Docker environment variables (DOCKER_HOST etc.).
// On macOS, if DOCKER_HOST is not set and the default socket does not exist,
// it auto-detects the Docker Desktop socket.
func New() (*Runner, error) {
	dockerHost := os.Getenv("DOCKER_HOST")

	// If DOCKER_HOST is not set, determine which socket path to use.
	if dockerHost == "" {
		// Try common socket locations in order.
		socketPaths := []string{
			"/var/run/docker.sock",                                    // Linux default
			filepath.Join(os.Getenv("HOME"), ".docker/run/docker.sock"), // macOS Docker Desktop
		}

		for _, socketPath := range socketPaths {
			if _, err := os.Stat(socketPath); err == nil {
				dockerHost = "unix://" + socketPath
				break
			}
		}

		// If no socket found and HOME is available, construct the macOS path explicitly
		if dockerHost == "" && os.Getenv("HOME") != "" {
			dockerHost = "unix://" + filepath.Join(os.Getenv("HOME"), ".docker/run/docker.sock")
		}
	}

	var opts []client.Opt
	if dockerHost != "" {
		opts = append(opts, client.WithHost(dockerHost))
	}
	opts = append(opts, client.WithAPIVersionNegotiation())

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Runner{cli: cli}, nil
}

// RunOptions configures a single sandboxed container execution.
type RunOptions struct {
	// Image is the Docker image to use (e.g. "node:20-slim").
	Image string

	// Cmd is the command and arguments to run inside the container.
	Cmd []string

	// RepoDir is the host path of the cloned repository (mounted read-only at /repo).
	RepoDir string

	// WorkDir is an optional host path for writable workspace (mounted at /workspace).
	// If empty, a temporary directory is created and cleaned up automatically.
	WorkDir string
}

// Run executes a container according to opts and returns the combined stdout+stderr.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, containerTimeout)
	defer cancel()

	// Ensure the image is available locally.
	if err := r.pullIfMissing(ctx, opts.Image); err != nil {
		return "", err
	}

	// Set up the writable workspace.
	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.MkdirTemp("", "fog-workspace-*")
		if err != nil {
			return "", fmt.Errorf("create workspace dir: %w", err)
		}
		defer os.RemoveAll(workDir)
	}

	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   opts.RepoDir,
			Target:   "/repo",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   workDir,
			Target:   "/workspace",
			ReadOnly: false,
		},
	}

	cfg := &container.Config{
		Image:       opts.Image,
		Cmd:         opts.Cmd,
		WorkingDir:  "/workspace",
		AttachStdout: true,
		AttachStderr: true,
	}

	hostCfg := &container.HostConfig{
		Mounts:      mounts,
		NetworkMode: container.NetworkMode("bridge"), // allow network for package installation; PAT stays on host
		AutoRemove:  false,  // we remove manually to collect logs first
		Resources: container.Resources{
			Memory:    memoryLimit,
			CPUPeriod: cpuPeriod,
			CPUQuota:  cpuQuota,
		},
		SecurityOpt: []string{"no-new-privileges"},
	}

	resp, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// Always remove the container when we are done.
	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rmCancel()
		_ = r.cli.ContainerRemove(rmCtx, resp.ID, container.RemoveOptions{Force: true})
	}()

	fmt.Fprintf(os.Stderr, "[fog] starting container (%s)...\n", opts.Image)
	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	// Stream logs while waiting for the container to exit.
	logStream, err := r.cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return "", fmt.Errorf("stream container logs: %w", err)
	}
	defer logStream.Close()

	// stdcopy.StdCopy demultiplexes the Docker stream (stripping binary headers)
	// and writes container stdout+stderr to both the capture buffer and host stderr.
	var buf bytes.Buffer
	w := io.MultiWriter(&buf, os.Stderr)
	if _, err := stdcopy.StdCopy(w, w, logStream); err != nil && ctx.Err() == nil {
		return "", fmt.Errorf("read container output: %w", err)
	}

	// Wait for exit status.
	statusCh, errCh := r.cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case waitErr := <-errCh:
		if waitErr != nil {
			return buf.String(), fmt.Errorf("wait for container: %w", waitErr)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			output := buf.String()
			if output != "" {
				return output, fmt.Errorf("container exited with status %d:\n%s", status.StatusCode, output)
			}
			return "", fmt.Errorf("container exited with status %d (no output)", status.StatusCode)
		}
	case <-ctx.Done():
		return buf.String(), fmt.Errorf("container timed out after %s", containerTimeout)
	}

	return buf.String(), nil
}

// pullIfMissing pulls an image if it is not already present on the daemon.
func (r *Runner) pullIfMissing(ctx context.Context, imageName string) error {
	images, err := r.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == imageName {
				return nil // already present
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[fog] pulling docker image %s...\n", imageName)
	out, err := r.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", imageName, err)
	}
	defer out.Close()
	_, _ = io.Copy(io.Discard, out) // consume to completion
	fmt.Fprintf(os.Stderr, "[fog] image %s ready\n", imageName)
	return nil
}
