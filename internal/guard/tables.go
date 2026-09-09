// 表白名单校验：提取 SQL 中引用的表名，确保全部在白名单内
package guard

import (
	"fmt"
	"strings"
)

// FROM 子句中终结"表清单"的关键字（遇到即停止收集逗号分隔的后续表）
var fromClauseEnders = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "LIMIT": true, "HAVING": true,
	"UNION": true, "ON": true, "USING": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "CROSS": true, "OUTER": true, "STRAIGHT_JOIN": true,
	"WINDOW": true, "FOR": true,
}

// CheckTables 校验 SQL 中通过 FROM/JOIN 引用的所有表都在白名单内。
// allowed 为空时不限制。表名比较大小写不敏感；schema.table 只比较表名部分。
// WITH 语句中定义的 CTE 名视为合法引用。
func CheckTables(sql string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	allowSet := map[string]bool{}
	for _, t := range allowed {
		allowSet[strings.ToLower(t)] = true
	}
	tokens := tokenize(sql)

	// 收集 CTE 名（WITH name AS (...), name2 AS (...)）加入临时白名单
	for i := 0; i < len(tokens); i++ {
		up := strings.ToUpper(tokens[i])
		if up == "WITH" || (up == "RECURSIVE" && i > 0 && strings.ToUpper(tokens[i-1]) == "WITH") {
			// WITH 后的标识符即 CTE 名；逗号后跟的下一个标识符同理
			j := i + 1
			for j < len(tokens) {
				if isIdent(tokens[j]) {
					allowSet[strings.ToLower(baseName(tokens[j]))] = true
					// 跳过到本 CTE 定义结束（配对括号后），看是否有逗号继续
					j = skipCTEBody(tokens, j)
					if j < len(tokens) && tokens[j] == "," {
						j++
						continue
					}
				}
				break
			}
		}
	}

	referenced := extractTables(tokens)
	for _, t := range referenced {
		if !allowSet[strings.ToLower(t)] {
			return fmt.Errorf("表 %s 不在允许查询的表白名单内（允许：%s）", t, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// extractTables 从 token 流提取 FROM/JOIN 后引用的表名
func extractTables(tokens []string) []string {
	var tables []string
	for i := 0; i < len(tokens); i++ {
		up := strings.ToUpper(tokens[i])
		if up != "FROM" && up != "JOIN" && up != "STRAIGHT_JOIN" {
			continue
		}
		j := i + 1
		// FROM 子句支持逗号分隔多表；JOIN 只取一个
		for j < len(tokens) {
			// 派生表 FROM (SELECT ...)：跳过（其内部的 FROM 会被外层循环独立处理）
			if tokens[j] == "(" {
				break
			}
			if isIdent(tokens[j]) {
				tables = append(tables, baseName(tokens[j]))
				j++
				// 跳过别名等，直到逗号（继续收表）或子句终结
				for j < len(tokens) {
					tk := tokens[j]
					if tk == "," {
						j++
						break
					}
					if tk == "(" || tk == ")" || fromClauseEnders[strings.ToUpper(tk)] {
						j = len(tokens) // FROM 清单结束
						break
					}
					j++
				}
				if up != "FROM" {
					break // JOIN 只收一个表
				}
				continue
			}
			break
		}
	}
	return tables
}

// skipCTEBody 从 CTE 名位置跳到其定义体（配对括号）之后
func skipCTEBody(tokens []string, start int) int {
	j := start + 1
	// 找到第一个 (
	for j < len(tokens) && tokens[j] != "(" {
		j++
	}
	depth := 0
	for ; j < len(tokens); j++ {
		if tokens[j] == "(" {
			depth++
		} else if tokens[j] == ")" {
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return j
}

// tokenize 把 SQL 拆成 token：标识符/关键字、括号、逗号；字符串字面量整体替换为占位符
func tokenize(sql string) []string {
	var tokens []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, word.String())
			word.Reset()
		}
	}
	runes := []rune(sql)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\'' || c == '"':
			flush()
			quote := c
			for i++; i < len(runes); i++ {
				if runes[i] == '\\' {
					i++
				} else if runes[i] == quote {
					break
				}
			}
			tokens = append(tokens, "'…'") // 字符串占位，不参与表名判断
		case c == '`':
			// 反引号标识符：读到闭合，内容作为标识符 token
			flush()
			var ident strings.Builder
			for i++; i < len(runes) && runes[i] != '`'; i++ {
				ident.WriteRune(runes[i])
			}
			tokens = append(tokens, ident.String())
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '.' || c == '$':
			word.WriteRune(c)
		case c == '(' || c == ')' || c == ',':
			flush()
			tokens = append(tokens, string(c))
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// isIdent 判断 token 是否像标识符（排除占位符、括号、逗号、纯数字）
func isIdent(t string) bool {
	if t == "" || t == "(" || t == ")" || t == "," || t == "'…'" {
		return false
	}
	allDigit := true
	for _, r := range t {
		if r < '0' || r > '9' {
			allDigit = false
			break
		}
	}
	return !allDigit
}

// baseName 取 schema.table 的表名部分
func baseName(t string) string {
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}
