package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/itchyny/gojq"
	"golang.org/x/exp/slog"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type healthcheckId int

type healthcheckJob struct {
	healthcheck HealthcheckQuery
	quit        chan struct{}
	ticker      *time.Ticker
}

type HealthcheckServer struct {
	healthchecks      map[healthcheckId]healthcheckJob
	wg                sync.WaitGroup
	nextHealthcheckId healthcheckId
	httpServer        *http.Server
}

func (h *HealthcheckServer) runJob(id healthcheckId) {
	defer h.wg.Done()
	job := h.healthchecks[id]
	for {
		select {
		case <-job.ticker.C:
			resp := job.healthcheck.check()
			var status string
			if resp.Status {
				status = "UP"
			} else {
				status = "DOWN"
			}
			slog.Info("healthcheck-done",
				slog.String("url", job.healthcheck.Url),
				slog.String("method", job.healthcheck.Method),
				slog.Int("expected-status", job.healthcheck.ExpectedStatus),
				slog.String("status", status),
			)

		case <-job.quit:
			job.ticker.Stop()
			return
		}
	}
}

var jobPathRegex = regexp.MustCompile("^/jobs/([0-9]+)$")

func (h *HealthcheckServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/jobs" || r.URL.Path == "/jobs/":
		switch r.Method {
		case http.MethodGet:
			h.handleGetAllJobs(w, r)
		case http.MethodPost:
			h.handleAddJob(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	case jobPathRegex.MatchString(r.URL.Path):
		matches := jobPathRegex.FindSubmatch([]byte(r.URL.Path))
		if matches == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		s, err := strconv.Atoi(string(matches[1]))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		jobId := healthcheckId(s)

		switch r.Method {
		case http.MethodGet:
			h.handleGetJob(w, r, jobId)
		case http.MethodDelete:
			h.handleDeleteJob(w, r, jobId)
		case http.MethodPut:
			h.handlePutJob(w, r, jobId)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *HealthcheckServer) handleGetAllJobs(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	var jobs []HealthcheckQuery
	for _, job := range h.healthchecks {
		jobs = append(jobs, job.healthcheck)
	}
	json.NewEncoder(w).Encode(jobs)
	return
}

func (h *HealthcheckServer) handleAddJob(w http.ResponseWriter, r *http.Request) {
	var healthcheck HealthcheckQuery
	err := json.NewDecoder(r.Body).Decode(&healthcheck)
	if err != nil {
		fmt.Printf("Error decoding json: %v\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	newId := h.AddHealthcheck(healthcheck)
	healthcheck.Id = newId
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(healthcheck)
	return
}

func (h *HealthcheckServer) handleGetJob(w http.ResponseWriter, r *http.Request, jobId healthcheckId) {
	job, ok := h.healthchecks[jobId]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(job.healthcheck)
	return
}

func (h *HealthcheckServer) handleDeleteJob(w http.ResponseWriter, r *http.Request, jobId healthcheckId) {
	h.StopHealthcheck(jobId)
	w.WriteHeader(http.StatusNoContent)
	return
}

func (h *HealthcheckServer) handlePutJob(w http.ResponseWriter, r *http.Request, jobId healthcheckId) {
	var healthcheck HealthcheckQuery
	err := json.NewDecoder(r.Body).Decode(&healthcheck)
	if err != nil {
		fmt.Printf("Error decoding json: %v\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	h.UpdateHealthcheck(jobId, healthcheck)
	w.WriteHeader(http.StatusOK)
	healthcheck.Id = jobId
	json.NewEncoder(w).Encode(healthcheck)
	return
}

func (h *HealthcheckServer) Run() {
	defer h.wg.Wait()
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handle)
	h.httpServer = &http.Server{
		Addr:    ":8081",
		Handler: mux,
	}
	err := h.httpServer.ListenAndServe()
	if err != nil {
		fmt.Printf("Error starting web server: %v\n", err)
	}
}

func NewHealthcheckServer() HealthcheckServer {
	return HealthcheckServer{
		healthchecks: make(map[healthcheckId]healthcheckJob),
	}
}

func (h *HealthcheckServer) AddHealthcheck(healthcheck HealthcheckQuery) healthcheckId {
	h.nextHealthcheckId++
	healthcheck.Id = h.nextHealthcheckId
	quit := make(chan struct{})
	ticker := time.NewTicker(healthcheck.Frequency)
	h.healthchecks[h.nextHealthcheckId] = healthcheckJob{
		healthcheck: healthcheck,
		quit:        quit,
		ticker:      ticker,
	}
	h.wg.Add(1)
	go h.runJob(h.nextHealthcheckId)
	return h.nextHealthcheckId
}

func (h *HealthcheckServer) UpdateHealthcheck(id healthcheckId, healthcheck HealthcheckQuery) {
	job, ok := h.healthchecks[id]
	if !ok {
		return
	}
	job.healthcheck = healthcheck
	job.ticker.Stop()
	job.ticker = time.NewTicker(healthcheck.Frequency)
	close(job.quit)
	job.quit = make(chan struct{})
	h.wg.Add(1)
	h.healthchecks[id] = job
	go h.runJob(id)
}

func (h *HealthcheckServer) StopHealthcheck(id healthcheckId) {
	job, ok := h.healthchecks[id]
	if !ok {
		return
	}
	close(job.quit)
	delete(h.healthchecks, id)
}

type JqQuery struct {
	Query       *gojq.Query
	Expectation string
}

func UnsafeNewJqQuery(query string, expectation string) JqQuery {
	q, err := gojq.Parse(query)
	if err != nil {
		panic(err)
	}
	return JqQuery{
		Query:       q,
		Expectation: expectation,
	}
}

type HealthcheckQuery struct {
	Id             healthcheckId
	Url            string
	Method         string
	ExpectedStatus int
	Frequency      time.Duration
	JqQuery        JqQuery
}

func (h HealthcheckQuery) MarshalJSON() ([]byte, error) {
	type marshalledJqQuery struct {
		Query       string `json:"query"`
		Expectation string `json:"expectation"`
	}
	var jqQuery *marshalledJqQuery
	if h.JqQuery.Query == nil {
		jqQuery = nil
	} else {
		jqQuery = &marshalledJqQuery{
			h.JqQuery.Query.String(),
			h.JqQuery.Expectation,
		}
	}
	return json.Marshal(struct {
		Id             healthcheckId      `json:"id"`
		Url            string             `json:"url"`
		Method         string             `json:"method"`
		ExpectedStatus int                `json:"expected_status"`
		Frequency      string             `json:"frequency"`
		JqQuery        *marshalledJqQuery `json:"jq_query,omitempty"`
	}{
		Id:             h.Id,
		Url:            h.Url,
		Method:         h.Method,
		ExpectedStatus: h.ExpectedStatus,
		Frequency:      h.Frequency.String(),
		JqQuery:        jqQuery,
	})
}

func (h *HealthcheckQuery) UnmarshalJSON(data []byte) error {
	type marshalledJqQuery struct {
		Query       string `json:"query"`
		Expectation string `json:"expectation"`
	}
	d := struct {
		Url            string             `json:"url"`
		Method         string             `json:"method"`
		ExpectedStatus int                `json:"expected_status"`
		Frequency      string             `json:"frequency"`
		JqQuery        *marshalledJqQuery `json:"jq_query"`
	}{
		Url:            "",
		Method:         "",
		ExpectedStatus: 0,
		Frequency:      "",
		JqQuery:        nil,
	}
	err := json.Unmarshal(data, &d)

	h.Url = d.Url
	h.Method = d.Method
	h.ExpectedStatus = d.ExpectedStatus
	h.Frequency, err = time.ParseDuration(d.Frequency)

	if err != nil {
		return err
	}
	if d.JqQuery == nil {
		h.JqQuery.Query = nil
	} else {
		q, err := gojq.Parse(d.JqQuery.Query)
		if err != nil {
			return err
		}
		h.JqQuery.Query = q
		h.JqQuery.Expectation = d.JqQuery.Expectation
	}
	return nil
}

type HealthcheckResponse struct {
	Status bool
}

func (h HealthcheckQuery) check() HealthcheckResponse {
	if h.Method != http.MethodGet {
		fmt.Printf("Error: method %s not supported\n", h.Method)
		return HealthcheckResponse{Status: false}
	}

	req, err := http.NewRequest(h.Method, h.Url, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return HealthcheckResponse{Status: false}
	}
	req.Header.Add("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return HealthcheckResponse{Status: false}
	}
	if resp.StatusCode != h.ExpectedStatus {
		fmt.Printf("Error: Unexpected status code, %d != %d\n", resp.StatusCode, h.ExpectedStatus)
		return HealthcheckResponse{Status: false}
	}
	if resp.StatusCode != h.ExpectedStatus {
		fmt.Printf("Error: Unexpected status code, %d != %d\n", resp.StatusCode, h.ExpectedStatus)
		return HealthcheckResponse{Status: false}
	}

	// Optionally check the response body against a jq query
	// We expect exactly one result
	if h.JqQuery.Query != nil {
		err = checkJSON(h, resp)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return HealthcheckResponse{Status: false}
		}
	}

	return HealthcheckResponse{Status: true}

}

func checkJSON(h HealthcheckQuery, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return errors.New("Error reading response body")
	}
	var data interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return errors.New("Error deserializing response body")
	}
	iter := h.JqQuery.Query.Run(data)

	v, ok := iter.Next()
	if !ok {
		return errors.New("Error parsing response body")
	}
	if v != h.JqQuery.Expectation {
		return errors.New("Expectation failed")
	}
	return nil
}

func main() {
	healthcheckServer := NewHealthcheckServer()
	healthcheckServer.Run()
}
