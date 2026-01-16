package main

import (
	"bufio"
	"fmt"
	"html/template"
	"os"
	"regexp"
	"strings"
	"time"
)

// TestResult 测试结果
type TestResult struct {
	Name     string
	Status   string // "PASS", "FAIL", "SKIP"
	Duration string
	Output   string
}

// TestReport 测试报告
type TestReport struct {
	Title       string
	GeneratedAt string
	Summary     struct {
		Total   int
		Passed  int
		Failed  int
		Skipped int
	}
	Tests    []TestResult
	Duration string
}

func main() {
	// 读取测试输出
	testOutput, err := readTestOutput("../reports/test_output.txt")
	if err != nil {
		fmt.Printf("Error reading test output: %v\n", err)
		os.Exit(1)
	}

	// 解析测试结果
	report := parseTestResults(testOutput)

	// 生成 HTML 报告
	if err := generateHTMLReport(report, "../reports/test_report.html"); err != nil {
		fmt.Printf("Error generating HTML report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ HTML 测试报告生成完成: reports/test_report.html")
}

// readTestOutput 读取测试输出文件
func readTestOutput(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var content strings.Builder
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		content.WriteString(scanner.Text() + "\n")
	}

	return content.String(), scanner.Err()
}

// parseTestResults 解析测试结果
func parseTestResults(output string) *TestReport {
	report := &TestReport{
		Title:       "NexKV TLA+ Verification 测试报告",
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Tests:       []TestResult{},
	}

	lines := strings.Split(output, "\n")
	currentTest := TestResult{}
	inTestOutput := false
	var testOutput strings.Builder

	// 解析测试输出
	for _, line := range lines {
		// 匹配测试开始: === RUN   TestXXX
		if matches := regexp.MustCompile(`=== RUN\s+(.+)`).FindStringSubmatch(line); len(matches) > 1 {
			if currentTest.Name != "" {
				report.Tests = append(report.Tests, currentTest)
			}
			currentTest = TestResult{Name: matches[1]}
			inTestOutput = false
			testOutput.Reset()
			continue
		}

		// 匹配测试结果: --- PASS: TestXXX (0.00s)
		if matches := regexp.MustCompile(`--- (PASS|FAIL|SKIP):\s+(\S+)\s+\((\d+\.\d+[sn|m|μ]+)\)`).FindStringSubmatch(line); len(matches) > 1 {
			currentTest.Status = matches[1]
			currentTest.Duration = matches[3]
			currentTest.Output = testOutput.String()
			inTestOutput = false

			// 更新统计
			report.Summary.Total++
			switch matches[1] {
			case "PASS":
				report.Summary.Passed++
			case "FAIL":
				report.Summary.Failed++
			case "SKIP":
				report.Summary.Skipped++
			}
			continue
		}

		// 匹配测试失败: FAIL: TestXXX
		if strings.HasPrefix(line, "FAIL:") {
			currentTest.Status = "FAIL"
			report.Summary.Failed++
		}

		// 收集测试输出
		if inTestOutput {
			testOutput.WriteString(line + "\n")
		}

		// 检测是否在测试输出中
		if strings.HasPrefix(line, "\t") || strings.Contains(line, "    ") {
			inTestOutput = true
		}
	}

	// 添加最后一个测试
	if currentTest.Name != "" {
		report.Tests = append(report.Tests, currentTest)
	}

	// 解析总耗时
	if matches := regexp.MustCompile(`ok\s+\S+\s+([\d.]+[sn|m|μ]+)`).FindStringSubmatch(output); len(matches) > 1 {
		report.Duration = matches[1]
	}

	return report
}

// generateHTMLReport 生成 HTML 报告
func generateHTMLReport(report *TestReport, filename string) error {
	tmpl := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            padding: 20px;
            line-height: 1.6;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
            background: white;
            border-radius: 10px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.3);
            overflow: hidden;
        }
        .header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 30px;
            text-align: center;
        }
        .header h1 {
            font-size: 2.5em;
            margin-bottom: 10px;
        }
        .header .meta {
            opacity: 0.9;
            font-size: 0.9em;
        }
        .summary {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            padding: 30px;
            background: #f8f9fa;
        }
        .summary-card {
            background: white;
            padding: 20px;
            border-radius: 8px;
            text-align: center;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        .summary-card h3 {
            color: #666;
            font-size: 0.9em;
            margin-bottom: 10px;
        }
        .summary-card .value {
            font-size: 2.5em;
            font-weight: bold;
        }
        .total { color: #667eea; }
        .passed { color: #28a745; }
        .failed { color: #dc3545; }
        .skipped { color: #ffc107; }
        .test-list {
            padding: 30px;
        }
        .test-list h2 {
            margin-bottom: 20px;
            color: #333;
        }
        .test-item {
            background: #f8f9fa;
            margin-bottom: 10px;
            padding: 15px;
            border-radius: 5px;
            border-left: 4px solid #667eea;
            transition: transform 0.2s;
        }
        .test-item:hover {
            transform: translateX(5px);
        }
        .test-item.pass { border-left-color: #28a745; }
        .test-item.fail { border-left-color: #dc3545; }
        .test-item.skip { border-left-color: #ffc107; }
        .test-name {
            font-weight: bold;
            color: #333;
            margin-bottom: 5px;
        }
        .test-meta {
            display: flex;
            gap: 15px;
            font-size: 0.9em;
            color: #666;
        }
        .status-pass { color: #28a745; }
        .status-fail { color: #dc3545; }
        .status-skip { color: #ffc107; }
        .test-output {
            margin-top: 10px;
            padding: 10px;
            background: white;
            border-radius: 3px;
            font-family: monospace;
            font-size: 0.85em;
            color: #555;
            overflow-x: auto;
            display: none;
        }
        .test-item:hover .test-output {
            display: block;
        }
        .footer {
            text-align: center;
            padding: 20px;
            color: #666;
            font-size: 0.9em;
            background: #f8f9fa;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>{{.Title}}</h1>
            <div class="meta">
                <p>生成时间: {{.GeneratedAt}}</p>
                {{if .Duration}}<p>总耗时: {{.Duration}}</p>{{end}}
            </div>
        </div>

        <div class="summary">
            <div class="summary-card">
                <h3>总测试数</h3>
                <div class="value total">{{.Summary.Total}}</div>
            </div>
            <div class="summary-card">
                <h3>通过</h3>
                <div class="value passed">{{.Summary.Passed}}</div>
            </div>
            <div class="summary-card">
                <h3>失败</h3>
                <div class="value failed">{{.Summary.Failed}}</div>
            </div>
            <div class="summary-card">
                <h3>跳过</h3>
                <div class="value skipped">{{.Summary.Skipped}}</div>
            </div>
        </div>

        <div class="test-list">
            <h2>测试详情</h2>
            {{range .Tests}}
            <div class="test-item {{if eq .Status "PASS"}}pass{{else if eq .Status "FAIL"}}fail{{else}}skip{{end}}">
                <div class="test-name">{{.Name}}</div>
                <div class="test-meta">
                    <span class="status-{{if eq .Status "PASS"}}pass{{else if eq .Status "FAIL"}}fail{{else}}skip{{end}}">
                        状态: {{.Status}}
                    </span>
                    {{if .Duration}}<span>耗时: {{.Duration}}</span>{{end}}
                </div>
                {{if .Output}}
                <div class="test-output"><pre>{{.Output}}</pre></div>
                {{end}}
            </div>
            {{end}}
        </div>

        <div class="footer">
            <p>NexKV TLA+ Verification - Transport Layer Testing</p>
        </div>
    </div>
</body>
</html>`

	t, err := template.New("report").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	if err := t.Execute(file, report); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}
