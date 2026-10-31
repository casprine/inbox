package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const forwardTo = "me@gmail.com"

// startWorker runs edge/e2e/harness.mjs (the real Worker code in Node) and an R2 stand-in that writes into
// the local S3 store. Returns a stop function.
func (d *driver) startWorker(repo string) (func(), error) {
	r2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := d.blob.Put(r.Context(), r.URL.Query().Get("key"), bytes.NewReader(b), int64(len(b)), nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	port, err := freePort()
	if err != nil {
		r2.Close()
		return nil, err
	}
	node := os.Getenv("E2E_NODE")
	if node == "" {
		node = "node"
	}
	harness := os.Getenv("E2E_HARNESS")
	if harness == "" {
		harness = filepath.Join(repo, "edge", "e2e", "harness.mjs")
	}
	cmd := exec.Command(node, "--experimental-strip-types", "--no-warnings", harness)
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port), "R2_PUT_URL="+r2.URL, "FORWARD_TO="+forwardTo,
		"INGEST_URL="+d.ingestBase+"/ingest/v1/notify", "INGEST_SECRET="+ingestSecret)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		r2.Close()
		return nil, err
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		r2.Close()
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			stop()
			return nil, fmt.Errorf("the Worker harness did not start (needs Node 22.6+): %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.workerURL = "http://127.0.0.1:" + strconv.Itoa(port)
	return stop, nil
}

// realWorkerDelivers hands one mail to the real Worker as Email Routing would and checks what the Worker did.
func (d *driver) realWorkerDelivers(name string, raw []byte, envTo string, storeOK bool, messageID string) error {
	req, _ := http.NewRequest("POST", d.workerURL+"/deliver", bytes.NewReader(raw))
	req.Header.Set("X-Env-From", "sender@example.org")
	req.Header.Set("X-Env-To", envTo)
	if !storeOK {
		req.Header.Set("X-Fail-R2", "1")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		Events    []string `json:"events"`
		StoredKey *string  `json:"stored_key"`
		Error     *string  `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.Error != nil {
		return fmt.Errorf("the Worker threw for %s (it must never): %s", name, *out.Error)
	}
	if len(out.Events) == 0 || out.Events[0] != "forward:"+forwardTo {
		return fmt.Errorf("the Worker must forward before anything else; events for %s: %v", name, out.Events)
	}
	if storeOK && out.StoredKey == nil {
		return fmt.Errorf("the Worker did not store %s: %v", name, out.Events)
	}
	if !storeOK && out.StoredKey != nil {
		return fmt.Errorf("the R2 outage was not simulated for %s", name)
	}
	d.samples[name] = sample{raw, messageID, envTo}
	return nil
}
