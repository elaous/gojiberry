package public

import (
	"context"
	"log"
	"sync"
	"time"
)

// prioWindow is how long a priority report counts against a reporter.
const prioWindow = 24 * time.Hour

// burstWindow is the hour over which priority reports count as a burst.
const burstWindow = time.Hour

// maxBurstNets bounds the networks the burst watch remembers.
const maxBurstNets = 10000

// prioTracker keeps the two counts that limit priority reports.
type prioTracker struct {
	mu      sync.Mutex
	byRep   map[string][]time.Time
	byNet   map[string]map[string]time.Time // net -> document -> last priority report
	flagged map[string]time.Time            // net -> when the burst was last logged
}

// allow reports whether reporter may make another priority report.
func (p *prioTracker) allow(reporter string, now time.Time, limit int) bool {
	if limit < 0 {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.recentLocked(reporter, now)) < limit
}

func (p *prioTracker) recentLocked(reporter string, now time.Time) []time.Time {
	ts := p.byRep[reporter]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < prioWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(p.byRep, reporter)
		return nil
	}
	p.byRep[reporter] = keep
	return keep
}

// record counts a stored priority report of reporter.
func (p *prioTracker) record(reporter string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byRep == nil {
		p.byRep = map[string][]time.Time{}
	}
	p.byRep[reporter] = append(p.byRep[reporter], at)
}

// rebuild replaces the allowance counts with the stored reports.
func (p *prioTracker) rebuild(info map[string]reportInfo, now, listStart time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fresh := map[string][]time.Time{}
	for _, in := range info {
		if in.priority && in.reporter != "" && now.Sub(in.at) < prioWindow {
			fresh[in.reporter] = append(fresh[in.reporter], in.at)
		}
	}
	for rep, ts := range p.byRep {
		for _, t := range ts {
			if !t.Before(listStart) {
				fresh[rep] = append(fresh[rep], t)
			}
		}
	}
	p.byRep = fresh
}

// note records a priority report from a network.
func (p *prioTracker) note(net, doc string, now time.Time, limit int) (over, first bool) {
	if limit <= 0 {
		return false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byNet == nil {
		p.byNet, p.flagged = map[string]map[string]time.Time{}, map[string]time.Time{}
	}
	docs := p.byNet[net]
	if docs == nil {
		if len(p.byNet) >= maxBurstNets {
			p.pruneLocked(now)
			if len(p.byNet) >= maxBurstNets {
				return false, false
			}
		}
		docs = map[string]time.Time{}
		p.byNet[net] = docs
	}
	docs[doc] = now
	for d, t := range docs {
		if now.Sub(t) >= burstWindow {
			delete(docs, d)
		}
	}
	if len(docs) <= limit {
		return false, false
	}
	if last, ok := p.flagged[net]; !ok || now.Sub(last) >= burstWindow {
		p.flagged[net] = now
		return true, true
	}
	return true, false
}

// bursts is how many networks are over the limit.
func (p *prioTracker) bursts(now time.Time, limit int) int {
	if limit <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, docs := range p.byNet {
		live := 0
		for _, t := range docs {
			if now.Sub(t) < burstWindow {
				live++
			}
		}
		if live > limit {
			n++
		}
	}
	return n
}

func (p *prioTracker) pruneLocked(now time.Time) {
	for rep := range p.byRep {
		p.recentLocked(rep, now)
	}
	for net, docs := range p.byNet {
		for d, t := range docs {
			if now.Sub(t) >= burstWindow {
				delete(docs, d)
			}
		}
		if len(docs) == 0 {
			delete(p.byNet, net)
		}
	}
	for net, t := range p.flagged {
		if now.Sub(t) >= burstWindow {
			delete(p.flagged, net)
		}
	}
}

// prune forgets what has aged out; Sweep calls it.
func (p *prioTracker) prune(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
}

// noteBurst feeds the burst watch for a priority report.
func (s *Service) noteBurst(ctx context.Context, net, doc string) {
	var over, first bool
	if s.burst != nil {
		var err error
		if h, herr := s.reporterHash(ctx, net); herr == nil {
			if over, first, err = s.burst.note(ctx, h, doc, s.now(), s.cfg.PriorityBurstDocs); err == nil {
				if over && first {
					log.Printf("public docstore: ALARM: priority reports for more than %d different documents within an hour from one network (%s): a wave of abuse, or the priority path being flooded; see priorityBursts in /admin/health", s.cfg.PriorityBurstDocs, net)
				}
				return
			}
		}
	}
	over, first = s.prio.note(net, doc, s.now(), s.cfg.PriorityBurstDocs)
	if over && first {
		log.Printf("public docstore: ALARM: priority reports for more than %d different documents within an hour from one network (%s): a wave of abuse, or the priority path being flooded; see priorityBursts in /admin/health", s.cfg.PriorityBurstDocs, net)
	}
}
