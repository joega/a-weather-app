package notifications

import "time"

type warningVertex struct {
	record            *warningRecord
	receipt           *WarningReceipt
	parents, children []*warningVertex
}

// buildWarningGraph includes placeholders for absent referenced bodies. They
// keep late-arriving ancestors connected to existing supersession/cancellation
// tombstones. Every edge is scoped to a location; provider identities alone
// cannot merge deliveries for different places. The graph must be acyclic,
// including valid same-second references (time ordering alone is insufficient).
func buildWarningGraph(d warningDocument) (map[string]*warningVertex, error) {
	graph := make(map[string]*warningVertex, len(d.Records))
	vertex := func(id string) *warningVertex {
		v := graph[id]
		if v == nil {
			v = &warningVertex{}
			graph[id] = v
		}
		return v
	}
	links := 0
	for i := range d.Records {
		r := &d.Records[i]
		v := vertex(warningID(r.Location, r.Key))
		if v.record != nil {
			return nil, warningError("duplicate identity")
		}
		v.record = r
		links += len(r.References)
		if len(graph) > warningNodeLimit || links > warningLinkLimit {
			return nil, ErrWarningLedgerCapacity
		}
		if r.Type == "Alert" {
			// Only Update and Cancel supersede referenced messages in CAP.
			continue
		}
		for _, key := range r.References {
			p := vertex(warningID(r.Location, key))
			v.parents = append(v.parents, p)
			p.children = append(p.children, v)
		}
		if len(graph) > warningNodeLimit || links > warningLinkLimit {
			return nil, ErrWarningLedgerCapacity
		}
	}
	for i := range d.Receipts {
		r := &d.Receipts[i]
		v := graph[warningID(r.Decision.Location, r.Decision.Key)]
		if v == nil || v.record == nil || v.receipt != nil {
			return nil, warningError("unmatched or duplicate receipt")
		}
		v.receipt = r
	}
	// Kahn's algorithm bounds traversal and rejects cycles without recursion.
	incoming := make(map[*warningVertex]int, len(graph))
	queue := make([]*warningVertex, 0, len(graph))
	for _, v := range graph {
		incoming[v] = len(v.parents)
		if len(v.parents) == 0 {
			queue = append(queue, v)
		}
		if v.record != nil {
			for _, p := range v.parents {
				if p.record != nil && p.record.Sent.After(v.record.Sent) {
					return nil, warningError("future reference")
				}
			}
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, child := range queue[i].children {
			incoming[child]--
			if incoming[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if len(queue) != len(graph) {
		return nil, warningError("cyclic references")
	}
	return graph, nil
}

// warningPriorReceipts finds the nearest attempted ancestor on each path. It
// deliberately stops at a receipt: A -> B -> A must report the return to A if
// B was delivered, rather than suppressing it against an older matching A.
func warningPriorReceipts(v *warningVertex) []*warningVertex {
	queue := append([]*warningVertex{}, v.parents...)
	seen := map[*warningVertex]bool{}
	out := []*warningVertex{}
	for i := 0; i < len(queue); i++ {
		p := queue[i]
		if seen[p] {
			continue
		}
		seen[p] = true
		if p.receipt != nil {
			out = append(out, p)
		} else {
			queue = append(queue, p.parents...)
		}
	}
	// Providers may reference the entire revision chain, adding direct edges
	// to older attempted ancestors as well as the immediate predecessor. Remove
	// those dominated receipts so an older material state cannot spuriously
	// turn an unchanged reissue into another update notification.
	queue = nil
	for _, p := range out {
		queue = append(queue, p.parents...)
	}
	ancestors := map[*warningVertex]bool{}
	for i := 0; i < len(queue); i++ {
		p := queue[i]
		if ancestors[p] {
			continue
		}
		ancestors[p] = true
		queue = append(queue, p.parents...)
	}
	frontier := []*warningVertex{}
	for _, p := range out {
		if !ancestors[p] {
			frontier = append(frontier, p)
		}
	}
	return frontier
}

func pruneWarningDocument(d warningDocument, now time.Time) warningDocument {
	graph, err := buildWarningGraph(d)
	if err != nil {
		return cloneWarningDocument(d)
	} // Internal input was validated.
	seen := map[*warningVertex]bool{}
	keep := map[*warningVertex]bool{}
	locations := map[string]bool{}
	for _, v := range graph {
		if seen[v] {
			continue
		}
		queue := []*warningVertex{v}
		seen[v] = true
		retain := false
		for i := 0; i < len(queue); i++ {
			p := queue[i]
			if p.record != nil && (p.record.Seen.Add(warningRetention).After(now) || p.record.Expires.After(now)) {
				retain = true
			}
			if p.receipt != nil && p.receipt.ReservedAt.Add(warningRetention).After(now) {
				retain = true
			}
			for _, edges := range [][]*warningVertex{p.parents, p.children} {
				for _, next := range edges {
					if !seen[next] {
						seen[next] = true
						queue = append(queue, next)
					}
				}
			}
		}
		if retain {
			for _, p := range queue {
				keep[p] = true
				if p.record != nil {
					locations[p.record.Location] = true
				}
			}
		}
	}
	out := warningDocument{Schema: d.Schema, UpdatedAt: d.UpdatedAt, Records: []warningRecord{}, Receipts: []WarningReceipt{}, Coverage: []warningCoverage{}}
	for _, r := range d.Records {
		if keep[graph[warningID(r.Location, r.Key)]] {
			out.Records = append(out.Records, r)
		}
	}
	for _, r := range d.Receipts {
		if keep[graph[warningID(r.Decision.Location, r.Decision.Key)]] {
			out.Receipts = append(out.Receipts, r)
		}
	}
	for _, c := range d.Coverage {
		if locations[c.Location] || c.FetchedAt.Add(warningRetention).After(now) {
			out.Coverage = append(out.Coverage, c)
		}
	}
	return out
}

// A cancellation on one branch must not imply the whole warning ended while
// another known terminal revision remains live. Keep both lifecycle records,
// but defer the cancellation interruption in this ambiguous branching case.
func warningHasLiveBranch(v *warningVertex, now time.Time) bool {
	queue := []*warningVertex{v}
	seen := map[*warningVertex]bool{v: true}
	for i := 0; i < len(queue); i++ {
		p := queue[i]
		if p.record != nil && p.record.Type != "Cancel" && len(p.children) == 0 && p.record.Expires.After(now) {
			return true
		}
		for _, edges := range [][]*warningVertex{p.parents, p.children} {
			for _, next := range edges {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return false
}
