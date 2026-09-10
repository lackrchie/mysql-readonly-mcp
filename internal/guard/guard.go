// SQL 安全防线（应用层）：语句白名单、拒多语句、显式黑名单
//
// 注意：这只是纵深防御的一层。服务端兜底在 store 层——每条查询在只读事务
// （START TRANSACTION READ ONLY）中执行，写操作会被 MySQL 服务端拒绝；
// 服务端不支持只读事务时，只读完全由本层白名单保证。
package guard

import (
	"fmt"
	"regexp"
	"strings"
)

// 无论白名单如何配置，这些模式一律拒绝：
// - INTO OUTFILE / INTO DUMPFILE：SELECT 也能写服务器文件，只读事务拦不住
// - FOR UPDATE / FOR SHARE / LOCK IN SHARE MODE：查询加锁，可能阻塞生产业务
var forbiddenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bINTO\s+(OUTFILE|DUMPFILE)\b`),
	regexp.MustCompile(`(?i)\bFOR\s+UPDATE\b`),
	regexp.MustCompile(`(?i)\bFOR\s+SHARE\b`),
	regexp.MustCompile(`(?i)\bLOCK\s+IN\s+SHARE\s+MODE\b`),
	regexp.MustCompile(`(?i)\bSLEEP\s*\(`),
	regexp.MustCompile(`(?i)\bBENCHMARK\s*\(`),
	regexp.MustCompile(`(?i)\bGET_LOCK\s*\(`),
	regexp.MustCompile(`(?i)\bLOAD_FILE\s*\(`),
}

// WITH 语句中出现在括号深度 0 的这些关键字说明 CTE 后面接的是写语句（MySQL 8 允许）
var writeKeywords = map[string]bool{
	"UPDATE": true, "DELETE": true, "INSERT": true, "REPLACE": true,
}

// Validate 校验 SQL 是否允许执行；返回清理后的 SQL（去掉首尾空白与末尾分号）
func Validate(sql string, allowed []string) (string, error) {
	cleaned := stripComments(strings.TrimSpace(sql))
	cleaned = strings.TrimRight(strings.TrimSpace(cleaned), ";")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", fmt.Errorf("SQL 为空")
	}
	// 拒绝任何剩余分号：不允许多语句（字符串字面量中的分号也一并拒绝，宁严勿松）
	if strings.Contains(cleaned, ";") {
		return "", fmt.Errorf("不允许多条语句或包含分号的 SQL（分号只能出现在语句末尾）")
	}

	// 首关键字白名单
	first := firstKeyword(cleaned)
	ok := false
	for _, a := range allowed {
		if strings.EqualFold(first, a) {
			ok = true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("语句类型 %s 不被允许，只支持只读查询：%s", first, strings.Join(allowed, "/"))
	}

	// 显式黑名单
	for _, p := range forbiddenPatterns {
		if loc := p.FindString(cleaned); loc != "" {
			return "", fmt.Errorf("SQL 包含被禁止的用法：%s", strings.TrimSpace(loc))
		}
	}

	// WITH 开头的语句：CTE 之后可能接 UPDATE/DELETE（MySQL 8 语法），检查括号深度 0 处的写关键字
	if strings.EqualFold(first, "WITH") {
		if kw := topLevelWriteKeyword(cleaned); kw != "" {
			return "", fmt.Errorf("WITH 语句中检测到写操作关键字 %s，不被允许", kw)
		}
	}
	return cleaned, nil
}

// stripComments 去掉 SQL 注释（-- 行注释、# 行注释、/* */ 块注释），
// 防止用注释伪装首关键字（如 /*!*/ DELETE）
func stripComments(sql string) string {
	var out strings.Builder
	runes := []rune(sql)
	n := len(runes)
	inSingle, inDouble, inBacktick := false, false, false
	for i := 0; i < n; i++ {
		c := runes[i]
		if inSingle {
			out.WriteRune(c)
			if c == '\\' && i+1 < n { // 跳过转义字符
				i++
				out.WriteRune(runes[i])
			} else if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			out.WriteRune(c)
			if c == '\\' && i+1 < n {
				i++
				out.WriteRune(runes[i])
			} else if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			out.WriteRune(c)
			if c == '`' {
				inBacktick = false
			}
			continue
		}
		switch {
		case c == '\'':
			inSingle = true
			out.WriteRune(c)
		case c == '"':
			inDouble = true
			out.WriteRune(c)
		case c == '`':
			inBacktick = true
			out.WriteRune(c)
		case c == '-' && i+1 < n && runes[i+1] == '-':
			// -- 行注释，跳到行尾
			for i < n && runes[i] != '\n' {
				i++
			}
			out.WriteRune(' ')
		case c == '#':
			for i < n && runes[i] != '\n' {
				i++
			}
			out.WriteRune(' ')
		case c == '/' && i+1 < n && runes[i+1] == '*':
			// 块注释（含 /*! */ 版本注释——它会被 MySQL 执行，必须整体去掉后拒绝其内容）
			end := strings.Index(string(runes[i+2:]), "*/")
			if end < 0 {
				return "" // 未闭合的注释，直接清空令校验失败
			}
			// 版本注释 /*! ... */ 内容会被执行，保留内容参与校验
			inner := string(runes[i+2 : i+2+end])
			if strings.HasPrefix(inner, "!") {
				out.WriteRune(' ')
				out.WriteString(strings.TrimLeft(inner, "!0123456789"))
				out.WriteRune(' ')
			} else {
				out.WriteRune(' ')
			}
			i += 2 + end + 1
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// firstKeyword 提取第一个词（字母）
func firstKeyword(sql string) string {
	fields := strings.FieldsFunc(sql, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// topLevelWriteKeyword 扫描括号深度为 0 处是否出现写操作关键字
func topLevelWriteKeyword(sql string) string {
	depth := 0
	inSingle, inDouble, inBacktick := false, false, false
	word := strings.Builder{}
	check := func() string {
		w := strings.ToUpper(word.String())
		word.Reset()
		if depth == 0 && writeKeywords[w] {
			return w
		}
		return ""
	}
	for _, c := range sql {
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
			}
			continue
		}
		switch {
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '`':
			inBacktick = true
		case c == '(':
			if kw := check(); kw != "" {
				return kw
			}
			depth++
		case c == ')':
			if kw := check(); kw != "" {
				return kw
			}
			depth--
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_':
			word.WriteRune(c)
			continue
		default:
			if kw := check(); kw != "" {
				return kw
			}
		}
	}
	return check()
}
