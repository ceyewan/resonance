package pilot

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这两条都是"论证依赖的前提"，靠人记不住，所以做成可执行的约束。

// Bot 最终消息只能有一个写入口。docs/14 里"FrozenFinalText 恒等于真实内容"的
// 论证完全建立在此之上：多一个写入点，这个恒等就静默失效了。
func TestFinalMessageHasExactlyOneWriter(t *testing.T) {
	root := repoRoot(t)
	var callSites []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "api", "web", "docs", "artifacts":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "CommitFinalMessage" {
				relative, _ := filepath.Rel(root, path)
				callSites = append(callSites, filepath.ToSlash(relative))
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pilot/coordinator/coordinator.go"}, callSites,
		"Bot 最终消息必须只有一个写入口；新增写入点前先读 docs/14 的提交闸门说明")
}

// 配置里的 bridge 协议版本必须和 bridge 源码自报的值一致，否则协议升级后旧快照
// 会被继续复用（写入 Run/Binding 的版本号是错的，且没有任何报错）。
func TestBridgeProtocolConfigMatchesBridgeSource(t *testing.T) {
	configValue := firstGroup(t,
		regexp.MustCompile(`(?m)^\s*expected_bridge_protocol:\s*"?([0-9]+)"?\s*$`),
		readRepoFile(t, "configs/pilot.yaml"),
		"configs/pilot.yaml 缺少 runtime.expected_bridge_protocol",
	)
	sourceValue := firstGroup(t,
		regexp.MustCompile(`bridge_protocol:\s*([0-9]+)`),
		readRepoFile(t, "pilot/bridge/src/index.ts"),
		"pilot/bridge/src/index.ts 不再自报 bridge_protocol",
	)
	require.Equal(t, sourceValue, configValue,
		"expected_bridge_protocol 必须与 bridge 源码的 bridge_protocol 同步修改")
}

func firstGroup(t *testing.T, pattern *regexp.Regexp, content, message string) string {
	t.Helper()
	match := pattern.FindStringSubmatch(content)
	require.Len(t, match, 2, message)
	return match[1]
}

func readRepoFile(t *testing.T, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(t), relative))
	require.NoError(t, err)
	return string(content)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		require.NotEqual(t, directory, parent, "go.mod not found above the test directory")
		directory = parent
	}
}
