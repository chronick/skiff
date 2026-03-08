package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chronick/skiff/internal/runtime"
	"github.com/chronick/skiff/internal/status"
)

// AdhocRunRequest is the JSON body for POST /v1/containers/run.
type AdhocRunRequest struct {
	Image      string            `json:"image"`
	Command    []string          `json:"command,omitempty"`
	Volumes    []string          `json:"volumes,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Ports      []string          `json:"ports,omitempty"`
	CPUs       float64           `json:"cpus,omitempty"`
	Memory     string            `json:"memory,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	TimeoutSec int               `json:"timeout_secs,omitempty"`
	Remove     bool              `json:"remove,omitempty"`
	Name       string            `json:"name,omitempty"`
	Network    string            `json:"network,omitempty"`
}

// AdhocRunResponse is the JSON response for POST /v1/containers/run.
type AdhocRunResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (d *Daemon) handleContainerRun(w http.ResponseWriter, r *http.Request) {
	var req AdhocRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Image == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image is required"})
		return
	}

	// Generate or validate name
	name := req.Name
	if name == "" {
		name = "adhoc-" + randomID()
	}

	// Check for name collision
	if _, ok := d.state.GetResource(name); ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("container %q already exists", name)})
		return
	}

	// Determine parent from request labels or default
	parent := ""
	if req.Labels != nil {
		parent = req.Labels["skiff.parent"]
	}

	// Build labels — merge user labels but reserve skiff.* prefix
	labels := make(map[string]string)
	for k, v := range req.Labels {
		labels[k] = v
	}
	// Always set the adhoc marker
	labels["skiff.adhoc"] = "true"
	if parent != "" {
		labels["skiff.parent"] = parent
	}

	// Build runtime config
	rtCfg := runtime.ContainerConfig{
		Image:   req.Image,
		Volumes: req.Volumes,
		Env:     req.Env,
		Ports:   req.Ports,
		CPUs:    req.CPUs,
		Memory:  req.Memory,
		Labels:  labels,
		Network: req.Network,
	}

	ctx := r.Context()

	// Apply resource limits if specified
	if req.CPUs > 0 || req.Memory != "" {
		rtCfg = d.runtime.SetLimits(rtCfg, runtime.ResourceLimits{
			CPUs:   req.CPUs,
			Memory: req.Memory,
		})
	}

	if err := d.runtime.Run(ctx, name, rtCfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Store in state with labels
	d.state.SetResource(&status.ResourceStatus{
		Name:      name,
		Type:      status.TypeContainer,
		State:     status.StateRunning,
		StartedAt: time.Now(),
		Ports:     req.Ports,
		Labels:    labels,
	})

	// Register with adhoc tracker for timeout/remove/parent tracking
	d.adhoc.Track(name, parent, req.Remove, req.TimeoutSec)

	d.logger.Info("ad-hoc container started",
		"name", name,
		"image", req.Image,
		"parent", parent,
		"timeout_secs", req.TimeoutSec,
		"remove", req.Remove,
	)

	writeJSON(w, http.StatusOK, AdhocRunResponse{
		Name:   name,
		Status: "started",
	})
}

func (d *Daemon) handleContainerStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	rs, ok := d.state.GetResource(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("container %q not found", name)})
		return
	}

	ctx := r.Context()

	if err := d.runtime.Stop(ctx, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Stop children if this is a parent
	d.adhoc.StopChildren(name)

	// Clean up adhoc tracking
	shouldRemove := d.adhoc.ShouldRemove(name)
	if d.adhoc.IsAdhoc(name) {
		d.adhoc.Untrack(name)
	}

	if shouldRemove {
		d.state.RemoveResource(name)
	} else {
		d.state.SetResource(&status.ResourceStatus{
			Name:   name,
			Type:   status.TypeContainer,
			State:  status.StateStopped,
			Labels: rs.Labels,
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"stopped": name})
}

// randomID generates a short random hex string for ad-hoc container names.
func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
