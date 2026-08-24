package risk

// setLevelIndicators name a SET of entities rather than asserting a
// relationship between each pair of them.
//
// Nearly every indicator in this package is pairwise-meaningful: when
// shared_person names five entities, each of the ten pairs really does
// share that person, so expanding the list to all pairs is a faithful
// reading of the evidence. A set-level indicator is not like that. Its
// entity list is a membership roll, and the pairs inside it were never
// claimed.
//
// entity_cluster is the case this exists for, and it is worse than
// merely uninformative: it is DERIVED from the other indicators, by
// union-find over their entity lists (see EntityCluster). Its members
// are transitively connected, not mutually connected, and whatever
// genuinely connects any two of them is already asserted by the
// indicator that produced the link. Treating its list as pairwise
// evidence therefore does two things, both wrong:
//
//   - It fabricates edges. One real 329-entity cluster expanded to
//     53,956 graph edges -- exactly 329*328/2, a complete clique --
//     which was 63% of every edge in the graph and buried the ~32,000
//     edges that carry actual evidence.
//
//   - It manufactures corroboration. A pair matched by one real
//     indicator plus this one looked like it had been "matched on 2+
//     independent kinds of evidence", which is what the report claims
//     of a corroborated pair. On the same scan that promoted 31,474
//     pairs; only 341 were genuinely corroborated -- 98.9% of them
//     were an artifact of counting a summary of the evidence as if it
//     were more evidence.
//
// This is the same failure the indicatorFamily map exists to prevent
// for convergent_risk: correlated signals counted as independent ones.
// convergent_risk itself is unaffected only by an accident of ordering
// -- Assess computes it one line before EntityCluster is appended, so
// it never sees the cluster, while computeCorroborations runs after and
// does.
//
// Membership is deliberately narrow. shared_person is NOT here despite
// being the next-largest edge producer, because its pairs are real; it
// is dense, not wrong, and density is a rendering question. Add a code
// here only when its entity list genuinely makes no pairwise claim.
var setLevelIndicators = map[string]bool{
	"entity_cluster": true,
}

// IsSetLevel reports whether an indicator code names a set of entities
// rather than asserting a relationship between each pair of them, and
// so must not be expanded pairwise.
//
// Exported because internal/graph needs the same answer that
// internal/risk does. Both call sites walk Indicator.Entities as all
// pairs, so a second copy of this list in the graph package would be
// free to drift out of step with this one -- and the bug this guards
// against is precisely two call sites disagreeing about what an entity
// list means.
func IsSetLevel(code string) bool { return setLevelIndicators[code] }
