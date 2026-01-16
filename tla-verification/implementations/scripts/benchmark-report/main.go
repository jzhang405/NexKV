package main

import (
	"bufio"
	"fmt"
	"html/template"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BenchmarkResult 单个 benchmark 结果
type BenchmarkResult struct {
	Name       string
	Iter       int64   // 迭代次数
	NsPerOp    float64 // 每次操作纳秒数
	MBPerSec   float64 // 每秒吞吐量(MB/s)
	BytesPerOp int64   // 每次操作字节数
	AllocsPerOp int64  // 每次操作分配次数
	Transport  string  // Transport 类型（从测试名称解析）
}

// BenchmarkReport 性能报告
type BenchmarkReport struct {
	Title       string
	GeneratedAt string
	Results     []BenchmarkResult
	Comparison  map[string][]BenchmarkResult // 按测试名称分组的结果
}

func main() {
	// 读取 benchmark 输出
	benchOutput, err := readBenchmarkOutput("../reports/benchmark/benchmark.txt")
	if err != nil {
		fmt.Printf("Error reading benchmark output: %v\n", err)
		os.Exit(1)
	}

	// 解析 benchmark 结果
	report := parseBenchmarkResults(benchOutput)

	// 生成 HTML 报告
	if err := generateHTMLReport(report, "../reports/benchmark/benchmark_report.html"); err != nil {
		fmt.Printf("Error generating HTML report: %v\n", err)
		os.Exit(1)
	}

	// 生成 Markdown 对比报告
	if err := generateMarkdownReport(report, "../reports/benchmark/benchmark_comparison.md"); err != nil {
		fmt.Printf("Error generating Markdown report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ 性能对比报告生成完成:")
	fmt.Println("   - reports/benchmark/benchmark_report.html")
	fmt.Println("   - reports/benchmark/benchmark_comparison.md")
}

// readBenchmarkOutput 读取 benchmark 输出文件
func readBenchmarkOutput(filename string) (string, error) {
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

// parseBenchmarkResults 解析 benchmark 结果
func parseBenchmarkResults(output string) *BenchmarkReport {
	report := &BenchmarkReport{
		Title:       "NexKV Transport 性能对比报告",
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Results:     []BenchmarkResult{},
		Comparison:  make(map[string][]BenchmarkResult),
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// 匹配 benchmark 行
		if strings.HasPrefix(line, "Benchmark") {
			result := parseBenchmarkLine(line)
			if result != nil {
				report.Results = append(report.Results, *result)

				// 按测试名称分组（去掉 Transport 类型）
				baseName := extractBaseName(result.Name)
				report.Comparison[baseName] = append(report.Comparison[baseName], *result)
			}
		}
	}

	return report
}

// parseBenchmarkLine 解析单行 benchmark 结果
func parseBenchmarkLine(line string) *BenchmarkResult {
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return nil
	}

	result := &BenchmarkResult{Name: parts[0]}

	// 解析迭代次数
	if iter, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
		result.Iter = iter
	}

	// 解析 ns/op
	if matches := regexp.MustCompile(`([\d.]+)\s+ns/op`).FindStringSubmatch(line); len(matches) > 1 {
		if ns, err := strconv.ParseFloat(matches[1], 64); err == nil {
			result.NsPerOp = ns
		}
	}

	// 解析 MB/s
	if matches := regexp.MustCompile(`([\d.]+)\s+MB/s`).FindStringSubmatch(line); len(matches) > 1 {
		if mb, err := strconv.ParseFloat(matches[1], 64); err == nil {
			result.MBPerSec = mb
		}
	}

	// 解析 B/op
	if matches := regexp.MustCompile(`(\d+)\s+B/op`).FindStringSubmatch(line); len(matches) > 1 {
		if b, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			result.BytesPerOp = b
		}
	}

	// 解析 allocs/op
	if matches := regexp.MustCompile(`(\d+)\s+allocs/op`).FindStringSubmatch(line); len(matches) > 1 {
		if a, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			result.AllocsPerOp = a
		}
	}

	// 解析 Transport 类型
	if matches := regexp.MustCompile(`Benchmark\w+_(Null|Memory|GRPC)Transport`).FindStringSubmatch(result.Name); len(matches) > 1 {
		transport := matches[1]
		if transport == "GRPC" {
			result.Transport = "gRPC"
		} else {
			result.Transport = transport
		}
	}

	return result
}

// extractBaseName 提取基础测试名称（去掉 Transport 类型）
func extractBaseName(fullName string) string {
	re := regexp.MustCompile(`Benchmark\w+_(Null|Memory|GRPC)Transport_(.+)`)
	matches := re.FindStringSubmatch(fullName)
	if len(matches) > 2 {
		return matches[2]
	}
	return fullName
}

// generateHTMLReport 生成 HTML 性能报告
func generateHTMLReport(report *BenchmarkReport, filename string) error {
	tmpl := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{.Title}}</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            padding: 20px;
        }
        .container {
            max-width: 1400px;
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
        .header h1 { font-size: 2.5em; margin-bottom: 10px; }
        .header .meta { opacity: 0.9; font-size: 0.9em; }
        .content { padding: 30px; }
        .comparison-section { margin-bottom: 40px; }
        .comparison-section h2 {
            color: #333;
            margin-bottom: 20px;
            padding-bottom: 10px;
            border-bottom: 2px solid #667eea;
        }
        .comparison-table {
            width: 100%;
            border-collapse: collapse;
            margin-bottom: 20px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        .comparison-table th {
            background: #667eea;
            color: white;
            padding: 15px;
            text-align: left;
            font-weight: 600;
        }
        .comparison-table td {
            padding: 12px 15px;
            border-bottom: 1px solid #eee;
        }
        .comparison-table tr:hover { background: #f8f9fa; }
        .best { color: #28a745; font-weight: bold; }
        .transport-badge {
            display: inline-block;
            padding: 4px 12px;
            border-radius: 12px;
            font-size: 0.85em;
            font-weight: 600;
        }
        .badge-null { background: #e7f3ff; color: #0066cc; }
        .badge-memory { background: #fff3e0; color: #e65100; }
        .badge-grpc { background: #f3e5f5; color: #7b1fa2; }
        .performance-bar {
            height: 30px;
            background: linear-gradient(90deg, #667eea 0%, #764ba2 100%);
            border-radius: 4px;
            position: relative;
            overflow: hidden;
        }
        .performance-bar span {
            position: absolute;
            right: 10px;
            top: 50%;
            transform: translateY(-50%);
            color: white;
            font-weight: bold;
            font-size: 0.9em;
        }
        .footer {
            text-align: center;
            padding: 20px;
            background: #f8f9fa;
            color: #666;
        }
        .no-data {
            text-align: center;
            padding: 40px;
            color: #999;
            font-style: italic;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>{{.Title}}</h1>
            <div class="meta">
                <p>生成时间: {{.GeneratedAt}}</p>
            </div>
        </div>

        <div class="content">
            {{if .Comparison}}
            {{range $baseName, $results := .Comparison}}
            <div class="comparison-section">
                <h2>{{$baseName}}</h2>
                <table class="comparison-table">
                    <thead>
                        <tr>
                            <th>Transport 类型</th>
                            <th>耗时 (ns/op)</th>
                            <th>吞吐量 (MB/s)</th>
                            <th>内存分配 (B/op)</th>
                            <th>分配次数 (allocs/op)</th>
                            <th>性能对比</th>
                        </tr>
                    </thead>
                    <tbody>
                        {{range $results}}
                        <tr>
                            <td>
                                {{if eq .Transport "Null"}}
                                <span class="transport-badge badge-null">Null</span>
                                {{else if eq .Transport "Memory"}}
                                <span class="transport-badge badge-memory">Memory</span>
                                {{else if eq .Transport "gRPC"}}
                                <span class="transport-badge badge-grpc">gRPC</span>
                                {{end}}
                            </td>
                            <td>{{printf "%.2f" .NsPerOp}}</td>
                            <td>{{if .MBPerSec}}{{printf "%.2f" .MBPerSec}}{{else}}—{{end}}</td>
                            <td>{{if .BytesPerOp}}{{.BytesPerOp}}{{else}}—{{end}}</td>
                            <td>{{if .AllocsPerOp}}{{.AllocsPerOp}}{{else}}—{{end}}</td>
                            <td>
                                <div class="performance-bar" style="width: {{calculateWidth .NsPerOp 500}}%">
                                    <span>{{printf "%.0f" .NsPerOp}} ns</span>
                                </div>
                            </td>
                        </tr>
                        {{end}}
                    </tbody>
                </table>
            </div>
            {{end}}
            {{else}}
            <div class="no-data">
                <p>暂无性能测试数据</p>
                <p>请运行 <code>make test-bench</code> 生成性能测试数据</p>
            </div>
            {{end}}
        </div>

        <div class="footer">
            <p>NexKV TLA+ Verification - Transport Layer Performance Comparison</p>
        </div>
    </div>
</body>
</html>`

	// 添加辅助函数
	funcMap := template.FuncMap{
		"calculateWidth": func(value, max float64) float64 {
			if value > max {
				return 100
			}
			return (value / max) * 100
		},
	}

	t, err := template.New("benchmark").Funcs(funcMap).Parse(tmpl)
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

// generateMarkdownReport 生成 Markdown 对比报告
func generateMarkdownReport(report *BenchmarkReport, filename string) error {
	var md strings.Builder

	md.WriteString("# " + report.Title + "\n\n")
	md.WriteString("**生成时间**: " + report.GeneratedAt + "\n\n")
	md.WriteString("---\n\n")

	md.WriteString("## 📊 性能对比摘要\n\n")
	md.WriteString("本报告对比了三种 Transport 实现的性能：\n\n")
	md.WriteString("| Transport 类型 | 特点 |\n")
	md.WriteString("|---------------|------|\n")
	md.WriteString("| **Null** | 零开销直接调用，性能基线 |\n")
	md.WriteString("| **Memory** | Channel 通信，适合快速测试 |\n")
	md.WriteString("| **gRPC** | 真实网络通信，生产环境 |\n")
	md.WriteString("\n---\n\n")

	// 按测试分组
	if len(report.Comparison) > 0 {
		for baseName, results := range report.Comparison {
			md.WriteString("### " + baseName + "\n\n")
			md.WriteString("| Transport | 耗时 (ns/op) | 吞吐量 (MB/s) | 内存 (B/op) | 分配次数 |\n")
			md.WriteString("|-----------|--------------|----------------|-------------|----------|\n")

			for _, r := range results {
				transport := r.Transport
				nsPerOp := fmt.Sprintf("%.2f", r.NsPerOp)
				mbPerSec := "—"
				if r.MBPerSec > 0 {
					mbPerSec = fmt.Sprintf("%.2f", r.MBPerSec)
				}
				bytesPerOp := "—"
				if r.BytesPerOp > 0 {
					bytesPerOp = fmt.Sprintf("%d", r.BytesPerOp)
				}
				allocsPerOp := "—"
				if r.AllocsPerOp > 0 {
					allocsPerOp = fmt.Sprintf("%d", r.AllocsPerOp)
				}

				md.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s | %s |\n",
					transport, nsPerOp, mbPerSec, bytesPerOp, allocsPerOp))
			}
			md.WriteString("\n")
		}
	} else {
		md.WriteString("> ⚠️ 暂无性能测试数据\n\n")
		md.WriteString("请运行 `make test-bench` 生成性能测试数据\n\n")
	}

	md.WriteString("---\n\n")
	md.WriteString("*报告由 NexKV 测试系统自动生成*\n")

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(md.String()); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}
