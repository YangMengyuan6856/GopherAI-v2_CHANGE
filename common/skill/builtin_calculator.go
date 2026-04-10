package skill

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"strings"
)

const CalculatorSkillCode = "calculator"

// CalculatorSkill 数学表达式求值技能，本地计算无外部依赖
type CalculatorSkill struct{}

func NewCalculatorSkill() *CalculatorSkill { return &CalculatorSkill{} }

func (c *CalculatorSkill) Code() string { return CalculatorSkillCode }
func (c *CalculatorSkill) Name() string { return "计算器" }
func (c *CalculatorSkill) Description() string {
	return "计算数学表达式，支持 +、-、*、/、()，示例：/skill calculator (1+2)*3"
}

func (c *CalculatorSkill) Execute(_ context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
	expr := req.Args["query"]
	if expr == "" {
		return &ExecuteResult{
			SkillCode: CalculatorSkillCode,
			Output:    "请提供要计算的数学表达式，示例：/skill calculator (1+2)*3",
		}, nil
	}

	expr = strings.ReplaceAll(expr, "×", "*")
	expr = strings.ReplaceAll(expr, "÷", "/")
	expr = strings.ReplaceAll(expr, "（", "(")
	expr = strings.ReplaceAll(expr, "）", ")")

	result, err := evalExpr(expr)
	if err != nil {
		return nil, fmt.Errorf("表达式计算失败: %w", err)
	}

	formatted := formatNumber(result)

	return &ExecuteResult{
		SkillCode: CalculatorSkillCode,
		Output:    fmt.Sprintf("%s = %s", expr, formatted),
		Data:      map[string]interface{}{"expression": expr, "result": result},
	}, nil
}

func evalExpr(expr string) (float64, error) {
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("无法解析表达式 \"%s\"", expr)
	}
	return evalNode(node)
}

func evalNode(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		if n.Kind != token.INT && n.Kind != token.FLOAT {
			return 0, fmt.Errorf("不支持的字面量类型: %s", n.Kind)
		}
		return strconv.ParseFloat(n.Value, 64)

	case *ast.ParenExpr:
		return evalNode(n.X)

	case *ast.UnaryExpr:
		x, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.SUB:
			return -x, nil
		case token.ADD:
			return x, nil
		default:
			return 0, fmt.Errorf("不支持的一元运算符: %s", n.Op)
		}

	case *ast.BinaryExpr:
		left, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("除数不能为零")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, fmt.Errorf("除数不能为零")
			}
			return math.Mod(left, right), nil
		default:
			return 0, fmt.Errorf("不支持的二元运算符: %s", n.Op)
		}

	default:
		return 0, fmt.Errorf("不支持的表达式类型")
	}
}

func formatNumber(v float64) string {
	if v == math.Trunc(v) && !math.IsInf(v, 0) && !math.IsNaN(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
