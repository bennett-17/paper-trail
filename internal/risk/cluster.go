package risk

import (
	"fmt"
	"sort"
	"strings"
)

// entityClusterThreshold is the minimum number of entities a connected
// component must contain before it's surfaced as its own indicator --
// one above convergentRiskThreshold's 3, since "these N entities are
// all transitively linked" is weaker per-entity evidence than "this
// one entity converges on 3+ distinct signal types" (ConvergentRisk),
// so the bar for calling attention to it is set slightly higher.
const entityClusterThreshold = 4

// entityClusterWeight is a flat weight per cluster found, regardless of
// its size -- deliberately below convergentRiskThreshold's minimum
// single-entity payout (3), for the same reason the threshold itself
// is higher: this is a weaker, network-shaped observation, not a
// per-entity convergence of independent evidence types.
const entityClusterWeight = 2

// EntityCluster is the graph-shaped analogue of ConvergentRisk: instead
// of asking "does this one entity independently show up in 3+ distinct
// kinds of indicator," it asks "how many entities are transitively
// connected to each other at all, through any combination of
// indicators." Every structural indicator that names 2+ entities (the
// same shape already used to draw graph.BuildFromRisk's edges, and the
// same shape computeCorroborations already walks pairwise) is treated
// as connecting those entities into one network; entities connected
// directly OR indirectly (A-B via one indicator, B-C via another) land
// in the same component even though A and C may never co-occur in any
// single indicator together.
//
// This is the one signal in this package that isn't visible from any
// pairwise view of the data at all: a reader can spot a
// shared_person hit between two companies, or even a Corroborated
// pair, without ever noticing that six such pairs chain together into
// one connected network -- exactly the kind of thing this session's
// own live scans turned up (a UK nominee director's appointments
// forming an 11-company web spanning three separate
// shared_person/shared_person_fuzzy/sequential_registration_numbers
// hits). Only components with entityClusterThreshold or more members
// are surfaced -- a 2-3 entity component is already fully visible as
// a Corroborated pair or a plain indicator list, so calling it out
// again here would be noise.
func EntityCluster(indicators []Indicator) []Indicator {
	uf := newUnionFind()
	// neighbors is the TRUE pairwise adjacency graph, built separately
	// from the union-find edges below -- union-find only needs a
	// spanning structure to find components (it links participants[0]
	// to each other participant, a star pattern, not every pair), which
	// would systematically undercount every non-participants[0] node's
	// degree if reused for degree centrality. This mirrors
	// computeCorroborations' own full pairwise walk (corroboration.go)
	// for exactly that reason: degree needs every edge an indicator
	// actually implies, not just enough of them to union entities
	// together.
	neighbors := map[string]map[string]bool{}
	for _, ind := range indicators {
		participants := dedupeStrings(ind.Entities)
		if len(participants) < 2 {
			continue
		}
		for _, p := range participants {
			uf.add(p)
		}
		for i := 1; i < len(participants); i++ {
			uf.union(participants[0], participants[i])
		}
		for i := 0; i < len(participants); i++ {
			for j := i + 1; j < len(participants); j++ {
				a, b := participants[i], participants[j]
				if neighbors[a] == nil {
					neighbors[a] = map[string]bool{}
				}
				if neighbors[b] == nil {
					neighbors[b] = map[string]bool{}
				}
				neighbors[a][b] = true
				neighbors[b][a] = true
			}
		}
	}

	members := uf.components()

	// Distinct indicator codes touching each final component, keyed by
	// root -- computed in a second pass now that union-find has
	// settled every entity into its final component, so a code
	// discovered via a late-processed indicator still attributes
	// correctly to the whole component it ended up part of.
	codesByRoot := map[string]map[string]bool{}
	for _, ind := range indicators {
		participants := dedupeStrings(ind.Entities)
		if len(participants) < 2 {
			continue
		}
		root := uf.find(participants[0])
		if codesByRoot[root] == nil {
			codesByRoot[root] = map[string]bool{}
		}
		codesByRoot[root][ind.Code] = true
	}

	roots := make([]string, 0, len(members))
	for root := range members {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	var out []Indicator
	for _, root := range roots {
		entities := members[root]
		if len(entities) < entityClusterThreshold {
			continue
		}
		sort.Strings(entities)

		codeSet := codesByRoot[root]
		codes := make([]string, 0, len(codeSet))
		for c := range codeSet {
			codes = append(codes, c)
		}
		sort.Strings(codes)

		// Hub: primarily the member with the most distinct direct
		// neighbors (degree centrality) -- a cheap, first-cut answer to
		// "which entity does this network actually hinge on," exactly
		// right for a star-shaped network (one nominee, many companies,
		// each only ever paired with the nominee). Degree alone can't
		// distinguish anyone in two real shapes: a fully-connected
		// clique (everyone tied at len(entities)-1) or a chain's
		// interior (every non-endpoint node tied at degree 2) -- when
		// 2+ members tie at the max degree, betweenness centrality (see
		// betweenness's own doc comment below) breaks the tie: in a
		// chain it correctly singles out the actual midpoint, since
		// only that member sits on shortest paths between members on
		// either side of it; in a clique it also ties (nobody sits
		// "between" anyone else there), which is the honest answer, not
		// a flaw. entities is already sorted, so any remaining tie
		// (including a full clique tie) falls back to the
		// lexicographically first member -- deterministic, not
		// otherwise significant.
		hub, hubDegree := "", -1
		var tiedAtMaxDegree []string
		for _, e := range entities {
			d := len(neighbors[e])
			switch {
			case d > hubDegree:
				hub, hubDegree = e, d
				tiedAtMaxDegree = []string{e}
			case d == hubDegree:
				tiedAtMaxDegree = append(tiedAtMaxDegree, e)
			}
		}
		if len(tiedAtMaxDegree) > 1 {
			bc := betweenness(neighbors, entities)
			bestBC := -1.0
			for _, e := range tiedAtMaxDegree { // already sorted -> first strictly-greater wins ties
				if bc[e] > bestBC {
					hub, bestBC = e, bc[e]
				}
			}
		}

		out = append(out, Indicator{
			Code:        "entity_cluster",
			Description: "These entities are all transitively connected -- directly or through a chain of intermediate entities -- by the structural indicators listed elsewhere in this report. A pairwise view (a single shared_person hit, a single Corroborated pair) can miss that several such links chain together into one larger network; this calls out the network itself as its own finding, on top of -- not instead of -- every indicator that produced it. The named hub is whichever member has the most distinct direct connections within the network (degree centrality) -- for a nominee-director-shaped network this is usually the nominee themselves, but it's a structural observation, not an accusation. Common for a large, legitimately multi-subsidiary organization as well as a shell-company network, so this is a map to investigate, not a verdict",
			Weight:      entityClusterWeight,
			Entities:    entities,
			Evidence:    fmt.Sprintf("%d entities connected via %s; hub: %s (degree %d)", len(entities), strings.Join(codes, ", "), hub, hubDegree),
		})
	}
	return out
}

// betweenness computes, for every member of one component, how often
// that member sits on a shortest path between two OTHER members --
// via Brandes' algorithm (BFS-based, O(V*E)).
//
// This comment used to claim clusters run "single digits to low tens of
// entities per scan, confirmed against this project's own largest real
// scans". That is no longer true and the confirmation had gone stale: a
// routine benchmark scan produces a single component of 329 entities,
// and larger scans go higher. It is measured here rather than asserted
// because it is the justification for running an O(V*E) algorithm at
// all.
//
// It remains affordable, for a reason worth stating: this is a
// tiebreaker, reached only when two or more members tie for maximum
// degree, and it runs over the component's REAL adjacency -- the
// underlying pairwise indicators -- not over any dense expansion of
// the cluster itself. Should this become hot, the fix is to bound the
// tiebreaker's input, not to widen the claim about cluster sizes.
//
// Only called as EntityCluster's hub tiebreaker (see its
// doc comment above), not as a standalone indicator of its own --
// degree centrality stays the primary, cheaper signal for the
// clear-cut star case, and this only runs when it's actually needed
// to disambiguate a tie.
//
// The graph is treated as unweighted and undirected (the same
// assumption degree centrality above already makes -- an indicator
// either connects two entities or it doesn't; this package has no
// notion of a stronger or weaker connection). Each shortest path
// between s and t is discovered once walking out from s and once
// walking out from t, so every accumulated value is halved at the end
// to avoid double-counting.
func betweenness(neighbors map[string]map[string]bool, members []string) map[string]float64 {
	inComponent := make(map[string]bool, len(members))
	for _, m := range members {
		inComponent[m] = true
	}
	centrality := make(map[string]float64, len(members))
	for _, m := range members {
		centrality[m] = 0
	}

	for _, s := range members {
		// Single-source BFS from s over the component, recording (per
		// Brandes) each node's distance from s, the number of distinct
		// shortest paths from s reaching it (sigma), and which
		// neighbors immediately precede it on some shortest path.
		var stack []string
		predecessors := map[string][]string{}
		sigma := map[string]int{s: 1}
		dist := map[string]int{s: 0}
		queue := []string{s}
		for len(queue) > 0 {
			v := queue[0]
			queue = queue[1:]
			stack = append(stack, v)

			neighborNames := make([]string, 0, len(neighbors[v]))
			for n := range neighbors[v] {
				if inComponent[n] {
					neighborNames = append(neighborNames, n)
				}
			}
			sort.Strings(neighborNames) // deterministic traversal order; doesn't affect the final scores, only tie-order within the BFS itself

			for _, w := range neighborNames {
				if _, seen := dist[w]; !seen {
					dist[w] = dist[v] + 1
					queue = append(queue, w)
				}
				if dist[w] == dist[v]+1 {
					sigma[w] += sigma[v]
					predecessors[w] = append(predecessors[w], v)
				}
			}
		}

		// Accumulate dependency scores back-to-front over the BFS
		// order -- Brandes' own key insight, avoiding the need to
		// enumerate every shortest path explicitly.
		delta := map[string]float64{}
		for i := len(stack) - 1; i >= 0; i-- {
			w := stack[i]
			for _, v := range predecessors[w] {
				delta[v] += (float64(sigma[v]) / float64(sigma[w])) * (1 + delta[w])
			}
			if w != s {
				centrality[w] += delta[w]
			}
		}
	}

	for m := range centrality {
		centrality[m] /= 2
	}
	return centrality
}

// RecomputeEntityCluster strips any existing entity_cluster indicators
// out of indicators and recomputes them fresh from what's left --
// mirrors RecomputeConvergentRisk, needed for the same reason: after
// cmd/paper-trail's risk --exclude permanently removes an indicator,
// a previously-computed cluster can reference an entity that's no
// longer actually connected to the rest, or a component that's now
// too small to clear entityClusterThreshold at all.
func RecomputeEntityCluster(indicators []Indicator) []Indicator {
	base := make([]Indicator, 0, len(indicators))
	for _, ind := range indicators {
		if ind.Code != "entity_cluster" {
			base = append(base, ind)
		}
	}
	return append(base, EntityCluster(base)...)
}

// unionFind is a minimal disjoint-set over entity labels (path
// compression, no union-by-rank -- the entity counts this package
// ever deals with, even a several-hundred-entity scan, are far too
// small for that to matter).
type unionFind struct {
	parent map[string]string
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	// path compression
	for u.parent[x] != root {
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// components returns every entity grouped by its component's root
// label, keyed by that root -- the root itself is just whichever label
// union() happened to settle on, not otherwise meaningful.
func (u *unionFind) components() map[string][]string {
	out := map[string][]string{}
	for x := range u.parent {
		root := u.find(x)
		out[root] = append(out[root], x)
	}
	return out
}
