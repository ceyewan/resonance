package eval

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessEvalDatasetAndScorer(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("testdata", "business_eval.json"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(suite.Cases), 8)

	observations := goldenObservations(suite)
	report, err := Evaluate(suite, observations)
	require.NoError(t, err)
	require.True(t, report.Passed)

	observations.Cases[0].Tools = append(observations.Cases[0].Tools, ToolObservation{Name: "bash", Status: "ok"})
	report, err = Evaluate(suite, observations)
	require.NoError(t, err)
	require.False(t, report.Passed)
	require.Contains(t, report.Cases[0].Failures, "tool sequence or terminal status mismatch")
}

func TestBusinessEvalFailsOnDuplicateOrUnreceiptedSideEffect(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("testdata", "business_eval.json"))
	require.NoError(t, err)
	observations := goldenObservations(suite)

	for index := range observations.Cases {
		if len(observations.Cases[index].SideEffects) != 0 {
			observations.Cases[index].SideEffects[0].ReceiptID = ""
			report, evalErr := Evaluate(suite, observations)
			require.NoError(t, evalErr)
			require.False(t, report.Passed)
			return
		}
	}
	t.Fatal("dataset must contain a side-effect case")
}

func TestBusinessEvalBindsObservationToImmutableCandidate(t *testing.T) {
	suite, err := LoadSuite(filepath.Join("testdata", "business_eval.json"))
	require.NoError(t, err)
	observations := goldenObservations(suite)

	report, err := EvaluateCandidate(suite, observations, observations.Runtime)
	require.NoError(t, err)
	require.True(t, report.Passed)

	mutable := goldenObservations(suite)
	mutable.Runtime.ControlImageDigest = "registry.example/resonance-pilot:latest"
	_, err = Evaluate(suite, mutable)
	require.ErrorContains(t, err, "pinned runtime record")

	sameImage := goldenObservations(suite)
	sameImage.Runtime.RuntimeImageDigest = "registry.example/different-repository@" +
		sameImage.Runtime.ControlImageDigest[strings.LastIndex(sameImage.Runtime.ControlImageDigest, "sha256:"):]
	_, err = Evaluate(suite, sameImage)
	require.ErrorContains(t, err, "pinned runtime record")

	expected := observations.Runtime
	expected.ProfileVersions = maps.Clone(expected.ProfileVersions)
	expected.ProfileVersions["iam-admin"]++
	_, err = EvaluateCandidate(suite, observations, expected)
	require.ErrorContains(t, err, "does not match the expected candidate release")

	expected = observations.Runtime
	expected.PiVersion = "0.84.2"
	_, err = EvaluateCandidate(suite, observations, expected)
	require.ErrorContains(t, err, "does not match the expected candidate release")
}

func goldenObservations(suite Suite) ObservationSet {
	set := ObservationSet{
		SuiteVersion: suite.Version,
		Runtime: RuntimeRecord{
			ControlImageDigest: "registry.example/resonance-pilot@sha256:" + strings.Repeat("a", 64),
			RuntimeImageDigest: "registry.example/resonance-pilot-runtime@sha256:" + strings.Repeat("b", 64),
			PiVersion:          "0.84.1", BridgeVersion: "eval-v1",
			ProfileVersions: map[string]int64{"user-assistant": 1, "iam-admin": 1},
		},
	}
	for _, testCase := range suite.Cases {
		observation := Observation{
			CaseID: testCase.ID, FinalText: "safe evaluated answer", QualityScore: 5,
			Refused: testCase.Expect.Refusal == "required",
		}
		for _, tool := range testCase.Expect.AllowedToolSequences[0] {
			observation.Tools = append(observation.Tools, ToolObservation(tool))
		}
		for _, expected := range testCase.Expect.SideEffects {
			for count := 0; count < expected.Count; count++ {
				observation.SideEffects = append(observation.SideEffects, SideEffectObservation{
					Kind: expected.Kind, IdempotencyKey: testCase.ID, ReceiptID: expected.Kind + "-receipt",
				})
			}
		}
		set.Cases = append(set.Cases, observation)
	}
	return set
}
