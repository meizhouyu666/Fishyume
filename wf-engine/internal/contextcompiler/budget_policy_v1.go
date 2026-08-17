package contextcompiler

// BudgetPolicyV1 is the internal, model-independent attention budget policy.
// Product callers do not provide this value through CLI/MCP; the compiler may
// also receive an already-resolved AttentionBudgetV2 for future drivers.
type BudgetPolicyV1 struct {
	TotalBytes     int `json:"totalBytes"`
	RequiredBytes  int `json:"requiredBytes"`
	ImportantBytes int `json:"importantBytes"`
	OptionalBytes  int `json:"optionalBytes"`
}

const (
	BudgetPolicyV1TotalBytes     = 128 * 1024
	BudgetPolicyV1RequiredBytes  = 64 * 1024
	BudgetPolicyV1ImportantBytes = 48 * 1024
	BudgetPolicyV1OptionalBytes  = 16 * 1024
)

func DefaultBudgetPolicyV1() BudgetPolicyV1 {
	return BudgetPolicyV1{TotalBytes: BudgetPolicyV1TotalBytes, RequiredBytes: BudgetPolicyV1RequiredBytes, ImportantBytes: BudgetPolicyV1ImportantBytes, OptionalBytes: BudgetPolicyV1OptionalBytes}
}

// StableBudgetPolicyV1 is kept as a value for fixture and compatibility tests.
var StableBudgetPolicyV1 = DefaultBudgetPolicyV1()

func (p BudgetPolicyV1) AttentionBudget() (AttentionBudgetV2, error) {
	budget := AttentionBudgetV2{TotalBytes: p.TotalBytes, RequiredBytes: p.RequiredBytes, ImportantBytes: p.ImportantBytes, OptionalBytes: p.OptionalBytes}
	if err := validateBudgetV2(budget); err != nil {
		return AttentionBudgetV2{}, err
	}
	return budget, nil
}

func ValidateBudgetPolicyV1(policy BudgetPolicyV1) error {
	want := DefaultBudgetPolicyV1()
	if policy != want {
		return contractError(CodeContextBudgetUnsatisfiable, "BudgetPolicyV1 must use the stable 128 KiB 4:3:1 default", "")
	}
	_, err := policy.AttentionBudget()
	return err
}
