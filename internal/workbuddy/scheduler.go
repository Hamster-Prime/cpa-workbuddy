package workbuddy

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// SchedulerStatus exposes the daily auto-checkin / daily-bonus schedule state.
type SchedulerStatus struct {
	Enabled        bool       `json:"enabled"`
	AnchorTime     string     `json:"anchor_time"`
	JitterRange    string     `json:"jitter_range"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastRunSummary string     `json:"last_run_summary,omitempty"`
	Running        bool       `json:"running"`
}

// EnsureScheduler starts the lifecycle-owned daily scheduler once a host callback is bound.
func (s *Service) EnsureScheduler(call func(string, []byte) ([]byte, error)) {
	if call == nil {
		return
	}
	s.bindHostCall(call)

	s.mu.Lock()
	closed := s.closed
	if closed {
		s.mu.Unlock()
		return
	}
	s.schedMu.Lock()
	if s.schedStarted {
		s.schedMu.Unlock()
		s.mu.Unlock()
		s.wakeScheduler()
		return
	}
	s.schedStarted = true
	s.jobs.Add(1)
	s.schedMu.Unlock()
	s.mu.Unlock()

	go func() {
		defer s.jobs.Done()
		s.runSchedulerLoop()
	}()
}

// SchedulerSnapshot returns the current daily scheduler status.
func (s *Service) SchedulerSnapshot() SchedulerStatus {
	cfg := s.Config()
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	st := s.schedState
	st.Enabled = cfg.AutoCheckinEnabled
	st.AnchorTime = cfg.AutoCheckinTime
	st.JitterRange = "±3h"
	return st
}

func (s *Service) wakeScheduler() {
	if s.schedWake == nil {
		return
	}
	select {
	case s.schedWake <- struct{}{}:
	default:
	}
}

func (s *Service) runSchedulerLoop() {
	var lastRunDate string
	var scheduledAnchor string
	var nextRun time.Time

	recompute := func(now time.Time) {
		cfg := s.Config()
		if !cfg.AutoCheckinEnabled {
			nextRun = time.Time{}
			scheduledAnchor = ""
			s.schedMu.Lock()
			s.schedState.Enabled = false
			s.schedState.AnchorTime = cfg.AutoCheckinTime
			s.schedState.JitterRange = "±3h"
			s.schedState.NextRunAt = nil
			s.schedMu.Unlock()
			return
		}
		if nextRun.IsZero() || scheduledAnchor != cfg.AutoCheckinTime || !nextRun.After(now) {
			nextRun = ComputeNextJitteredRun(now, cfg.AutoCheckinTime, lastRunDate, cryptoJitterOffset)
			scheduledAnchor = cfg.AutoCheckinTime
		}
		nCopy := nextRun
		s.schedMu.Lock()
		s.schedState.Enabled = true
		s.schedState.AnchorTime = cfg.AutoCheckinTime
		s.schedState.JitterRange = "±3h"
		s.schedState.NextRunAt = &nCopy
		s.schedMu.Unlock()
	}

	recompute(time.Now())

	for {
		var timer *time.Timer
		var timerCh <-chan time.Time
		if !nextRun.IsZero() {
			wait := max(time.Until(nextRun), time.Second)
			timer = time.NewTimer(wait)
			timerCh = timer.C
		}

		select {
		case <-s.execCtx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.schedWake:
			if timer != nil {
				timer.Stop()
			}
			recompute(time.Now())
		case <-timerCh:
			now := time.Now()
			cfg := s.Config()
			if !cfg.AutoCheckinEnabled {
				recompute(now)
				continue
			}

			lastRunDate = now.Format("2006-01-02")
			s.executeScheduledRun()
			nextRun = time.Time{}
			recompute(time.Now())
		}
	}
}

func (s *Service) executeScheduledRun() {
	call := s.currentHostCall()
	if call == nil {
		return
	}

	s.schedMu.Lock()
	s.schedState.Running = true
	s.schedMu.Unlock()

	runCtx, cancel := context.WithTimeout(s.execCtx, 3*time.Minute)
	results, err := s.RunAllDailyActions(runCtx, "", call, "all")
	cancel()

	finishedAt := time.Now().UTC()
	summary := summarizeDailyResults(results, err)

	s.schedMu.Lock()
	s.schedState.Running = false
	s.schedState.LastRunAt = &finishedAt
	s.schedState.LastRunSummary = summary
	s.schedMu.Unlock()

	emitHostLog(call, "", "info", "WorkBuddy daily scheduled check-in/bonus finished", map[string]any{
		"summary": summary,
		"count":   len(results),
	})
}

func summarizeDailyResults(results []DailyActionResult, err error) string {
	if err != nil {
		return "执行失败: " + err.Error()
	}
	if len(results) == 0 {
		return "无可用账号"
	}
	var claimed, lit, done, failed int
	for _, r := range results {
		switch r.Outcome {
		case "claimed":
			claimed++
		case "lit":
			lit++
		case "done", "already_lit":
			done++
		case "failed":
			failed++
		}
	}
	return fmt.Sprintf("总计 %d 个账号：新签到 %d，新点亮 %d，已完成 %d，失败 %d", len(results), claimed, lit, done, failed)
}

// ComputeNextJitteredRun picks the next execution timestamp at anchorTime (HH:MM)
// with +/-3h random jitter, rolling over to tomorrow if today already ran or today's
// [anchor-3h, anchor+3h] window has elapsed.
func ComputeNextJitteredRun(now time.Time, anchorTime, lastRunDate string, jitterSeconds func(minSec, maxSec int64) int64) time.Time {
	hour, minute, err := parseClockHHMM(anchorTime)
	if err != nil {
		hour, minute = 9, 0
	}
	if jitterSeconds == nil {
		jitterSeconds = cryptoJitterOffset
	}

	loc := now.Location()
	todayStr := now.Format("2006-01-02")
	baseToday := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	windowEndToday := baseToday.Add(AutoCheckinJitter * time.Second)

	if lastRunDate != todayStr && windowEndToday.After(now) {
		minOffset := int64(-AutoCheckinJitter)
		earliestFromNow := int64(now.Add(time.Minute).Sub(baseToday).Seconds())
		if earliestFromNow > minOffset {
			minOffset = earliestFromNow
		}
		if minOffset <= AutoCheckinJitter {
			offset := jitterSeconds(minOffset, AutoCheckinJitter)
			candidate := baseToday.Add(time.Duration(offset) * time.Second)
			if candidate.After(now) {
				return candidate
			}
			return now.Add(time.Minute)
		}
	}

	tomorrow := baseToday.AddDate(0, 0, 1)
	offset := jitterSeconds(-AutoCheckinJitter, AutoCheckinJitter)
	return tomorrow.Add(time.Duration(offset) * time.Second)
}

func cryptoJitterOffset(minSec, maxSec int64) int64 {
	if maxSec <= minSec {
		return minSec
	}
	span := maxSec - minSec + 1
	n, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return (minSec + maxSec) / 2
	}
	return minSec + n.Int64()
}
