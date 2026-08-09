// Package agentmutation 定义 Agent IAM 写操作跨 Pilot/Logic 共用的规范化绑定。
package agentmutation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ceyewan/resonance/model"
)

const (
	MembershipStatusTool          = "set_tenant_member_status"
	MembershipStatusToolVersion   = "1"
	MembershipStatusSchemaVersion = "1"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// MembershipStatusArgs 是审批和执行共同绑定的完整参数。
// TenantID/RunID/CallID 由已验证 Capability 注入，而不是接受模型提供的值。
type MembershipStatusArgs struct {
	Domain          string `json:"domain"`
	TenantID        string `json:"tenant_id"`
	RunID           string `json:"run_id"`
	CallID          string `json:"call_id"`
	ToolName        string `json:"tool_name"`
	RequesterID     string `json:"requester_id"`
	TargetUsername  string `json:"target_username"`
	DesiredStatus   string `json:"desired_status"`
	ExpectedVersion int64  `json:"expected_version"`
	DryRun          bool   `json:"dry_run"`
}

func NewMembershipStatusArgs(tenantID, runID, callID, requesterID, targetUsername, desiredStatus string, expectedVersion int64, dryRun bool) MembershipStatusArgs {
	return MembershipStatusArgs{
		Domain: "resonance.agent.iam.membership-status.v1", TenantID: tenantID, RunID: runID,
		CallID: callID, ToolName: MembershipStatusTool, RequesterID: requesterID, TargetUsername: strings.TrimSpace(targetUsername),
		DesiredStatus: strings.ToUpper(strings.TrimSpace(desiredStatus)), ExpectedVersion: expectedVersion, DryRun: dryRun,
	}
}

func (a MembershipStatusArgs) Validate() error {
	if a.Domain != "resonance.agent.iam.membership-status.v1" || a.ToolName != MembershipStatusTool {
		return fmt.Errorf("invalid membership mutation domain or tool")
	}
	if !identifierPattern.MatchString(a.TenantID) || !identifierPattern.MatchString(a.RunID) ||
		!identifierPattern.MatchString(a.CallID) || len(a.CallID) > 128 ||
		!identifierPattern.MatchString(a.RequesterID) || len(a.RequesterID) > 64 ||
		!identifierPattern.MatchString(a.TargetUsername) || len(a.TargetUsername) > 64 {
		return fmt.Errorf("invalid membership mutation identifier")
	}
	if a.DesiredStatus != model.TenantMembershipStatusActive && a.DesiredStatus != model.TenantMembershipStatusDisabled {
		return fmt.Errorf("desired_status must be ACTIVE or DISABLED")
	}
	if a.ExpectedVersion < 1 {
		return fmt.Errorf("expected_version must be positive")
	}
	return nil
}

func (a MembershipStatusArgs) CanonicalPayload() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("marshal membership mutation: %w", err)
	}
	return payload, nil
}

func (a MembershipStatusArgs) Hash() (string, error) {
	payload, err := a.CanonicalPayload()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func ParseMembershipStatusArgs(payload []byte) (MembershipStatusArgs, error) {
	var args MembershipStatusArgs
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return MembershipStatusArgs{}, fmt.Errorf("decode membership mutation: %w", err)
	}
	if err := args.Validate(); err != nil {
		return MembershipStatusArgs{}, err
	}
	canonical, err := args.CanonicalPayload()
	if err != nil {
		return MembershipStatusArgs{}, err
	}
	if string(canonical) != string(payload) {
		return MembershipStatusArgs{}, fmt.Errorf("membership mutation payload is not canonical")
	}
	return args, nil
}

func IdempotencyKey(tenantID, callID string) string {
	return "agent-iam-membership:v1:" + tenantID + ":" + callID
}

func FrozenArgsRef(tenantID, callID, argsHash string) string {
	return "db://agent-frozen-args/" + tenantID + "/" + callID + "/" + argsHash
}
