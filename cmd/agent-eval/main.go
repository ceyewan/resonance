package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	agenteval "github.com/ceyewan/resonance/pilot/eval"
)

func main() {
	var dataset string
	var observations string
	var controlImage string
	var runtimeImage string
	var piVersion string
	var bridgeVersion string
	profileVersions := make(profileVersionFlags)
	flag.StringVar(&dataset, "dataset", "pilot/eval/testdata/business_eval.json", "versioned business eval dataset")
	flag.StringVar(&observations, "observations", "", "live observation JSON produced by the canary runner")
	flag.StringVar(&controlImage, "control-image", "", "expected immutable control image digest reference")
	flag.StringVar(&runtimeImage, "runtime-image", "", "expected immutable runtime image digest reference")
	flag.StringVar(&piVersion, "pi-version", "", "expected exact Pi version")
	flag.StringVar(&bridgeVersion, "bridge-version", "", "expected exact trusted Bridge version")
	flag.Var(profileVersions, "profile-version", "expected profile version as profile-id=positive-version (repeatable)")
	flag.Parse()
	if observations == "" || controlImage == "" || runtimeImage == "" || piVersion == "" ||
		bridgeVersion == "" || len(profileVersions) == 0 {
		fmt.Fprintln(os.Stderr, "observations and the complete expected candidate runtime are required")
		os.Exit(2)
	}
	suite, err := agenteval.LoadSuite(dataset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load dataset: %v\n", err)
		os.Exit(2)
	}
	observed, err := agenteval.LoadObservations(observations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load observations: %v\n", err)
		os.Exit(2)
	}
	report, err := agenteval.EvaluateCandidate(suite, observed, agenteval.RuntimeRecord{
		ControlImageDigest: controlImage,
		RuntimeImageDigest: runtimeImage,
		PiVersion:          piVersion,
		BridgeVersion:      bridgeVersion,
		ProfileVersions:    profileVersions,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluate: %v\n", err)
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
	if !report.Passed {
		os.Exit(1)
	}
}

type profileVersionFlags map[string]int64

func (values profileVersionFlags) String() string {
	return fmt.Sprint(map[string]int64(values))
}

func (values profileVersionFlags) Set(value string) error {
	profileID, versionText, ok := strings.Cut(value, "=")
	if !ok || profileID == "" || strings.TrimSpace(profileID) != profileID {
		return fmt.Errorf("profile version must use profile-id=positive-version")
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version < 1 {
		return fmt.Errorf("profile version must use profile-id=positive-version")
	}
	if _, exists := values[profileID]; exists {
		return fmt.Errorf("profile version %q was provided more than once", profileID)
	}
	values[profileID] = version
	return nil
}
