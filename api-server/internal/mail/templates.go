package mail

import (
	"fmt"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/lang"
)

// PasswordResetEmail renders the localized password reset message. link must be
// built from the configured panel URL by the caller, never from request data.
// Any language other than ko or zh is rendered in English.
func PasswordResetEmail(language lang.Code, link string, validFor time.Duration) (subject, body string) {
	minutes := int(validFor / time.Minute)
	switch language {
	case lang.Korean:
		return "Proxima 비밀번호 재설정 안내",
			fmt.Sprintf("안녕하세요.\n\n"+
				"Proxima 계정의 비밀번호 재설정 요청이 접수되었습니다.\n"+
				"아래 링크에서 새 비밀번호를 설정해 주세요. 링크는 %d분 동안만 유효하며 한 번만 사용할 수 있습니다.\n\n"+
				"%s\n\n"+
				"본인이 요청하지 않으셨다면 이 메일을 무시해 주세요. 비밀번호는 변경되지 않습니다.\n", minutes, link)
	case lang.Chinese:
		return "重置您的 Proxima 密码",
			fmt.Sprintf("您好，\n\n"+
				"我们收到了重置您 Proxima 账户密码的请求。\n"+
				"请通过下方链接设置新密码。该链接 %d 分钟内有效，且只能使用一次。\n\n"+
				"%s\n\n"+
				"如果这不是您本人的操作，请忽略此邮件，您的密码不会被更改。\n", minutes, link)
	default:
		return "Reset your Proxima password",
			fmt.Sprintf("Hello,\n\n"+
				"We received a request to reset the password for your Proxima account.\n"+
				"Open the link below to choose a new password. The link is valid for %d minutes and can be used only once.\n\n"+
				"%s\n\n"+
				"If you did not request this, you can safely ignore this email. Your password will not change.\n", minutes, link)
	}
}
