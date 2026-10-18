package handlers

import (
	"fmt"
	"strings"
)

func signupVerificationSubject(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ko":
		return "Proxima Portal 이메일 인증 코드"
	case "zh":
		return "Proxima Portal 邮箱验证码"
	default:
		return "Proxima Portal email verification code"
	}
}

func signupVerificationBody(language, code string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ko":
		return fmt.Sprintf("Proxima Portal 인증 코드는 %s입니다. 10분 동안 유효합니다. 요청하지 않으셨다면 이 메일을 무시하셔도 됩니다.", code)
	case "zh":
		return fmt.Sprintf("您的 Proxima Portal 验证码是 %s，10 分钟内有效。如果您没有请求此验证码，请忽略此邮件。", code)
	default:
		return fmt.Sprintf("Your Proxima Portal verification code is %s. It expires in 10 minutes. If you did not request this, you can ignore this message.", code)
	}
}
