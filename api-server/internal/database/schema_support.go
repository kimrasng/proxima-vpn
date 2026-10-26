package database

// Support tickets are the conversation between a user and the operators. They
// are unrelated to reset_tickets (a data-reset coupon). A category groups the
// pre-written FAQ entries shown before a user opens a ticket; names, questions
// and answers carry one text per language so the wizard renders in the user's
// own language.
var supportSchema = []string{
	`CREATE TABLE IF NOT EXISTS support_categories (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		slug        TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9-]{1,40}$'),
		names       JSONB NOT NULL DEFAULT '{}'::jsonb,
		sort_order  INT NOT NULL DEFAULT 0,
		is_active   BOOLEAN NOT NULL DEFAULT TRUE,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS support_faqs (
		id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		category_id  UUID NOT NULL REFERENCES support_categories(id) ON DELETE CASCADE,
		questions    JSONB NOT NULL DEFAULT '{}'::jsonb,
		answers      JSONB NOT NULL DEFAULT '{}'::jsonb,
		sort_order   INT NOT NULL DEFAULT 0,
		is_active    BOOLEAN NOT NULL DEFAULT TRUE,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_support_faqs_category ON support_faqs(category_id, sort_order)`,
	// status: open = waiting for an operator, answered = an operator replied,
	// closed = finished. The *_unread flags drive the badges on each side.
	`CREATE TABLE IF NOT EXISTS support_tickets (
		id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		category_id    UUID REFERENCES support_categories(id) ON DELETE SET NULL,
		subject        TEXT NOT NULL CHECK (BTRIM(subject) <> ''),
		status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'answered', 'closed')),
		priority       TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high')),
		user_unread    BOOLEAN NOT NULL DEFAULT FALSE,
		admin_unread   BOOLEAN NOT NULL DEFAULT TRUE,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_reply_at  TIMESTAMPTZ,
		closed_at      TIMESTAMPTZ
	)`,
	`CREATE INDEX IF NOT EXISTS idx_support_tickets_user ON support_tickets(user_id, updated_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets(status, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS support_messages (
		id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		ticket_id    UUID NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
		author_type  TEXT NOT NULL CHECK (author_type IN ('user', 'admin')),
		author_label TEXT NOT NULL DEFAULT '',
		body         TEXT NOT NULL CHECK (BTRIM(body) <> ''),
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_support_messages_ticket ON support_messages(ticket_id, created_at)`,
	// Starter answers are seeded once, guarded by a settings marker, so an
	// operator who deletes them never sees them come back on restart.
	`INSERT INTO support_categories (slug, names, sort_order) VALUES
		('billing',  '{"ko":"구매 및 환불","en":"Purchase & refunds","zh":"购买与退款"}', 10),
		('plans',    '{"ko":"요금제·데이터·속도","en":"Plans, data & speed","zh":"套餐、流量与速度"}', 20),
		('clients',  '{"ko":"사용법 및 클라이언트","en":"Usage & clients","zh":"使用方法与客户端"}', 30),
		('apps',     '{"ko":"웹사이트 및 앱","en":"Website & apps","zh":"网站与应用"}', 40),
		('account',  '{"ko":"계정 및 보안","en":"Account & security","zh":"账户与安全"}', 50),
		('referral', '{"ko":"초대 및 커미션","en":"Invites & commission","zh":"邀请与佣金"}', 60)
	 ON CONFLICT (slug) DO NOTHING`,
	`WITH marker AS (
		INSERT INTO settings (key, value) VALUES ('support_faqs_seeded', 'true')
		ON CONFLICT (key) DO NOTHING RETURNING key
	 )
	 INSERT INTO support_faqs (category_id, questions, answers, sort_order)
	 SELECT c.id, f.questions::jsonb, f.answers::jsonb, f.sort_order
	 FROM (VALUES
		('billing', 10,
		 '{"ko":"결제했는데 요금제가 적용되지 않아요.","en":"I paid but my plan is not active.","zh":"已付款但套餐未生效。"}',
		 '{"ko":"결제 확인에는 보통 1~2분이 걸립니다. 5분이 지나도 적용되지 않으면 주문 번호와 함께 티켓을 남겨 주세요.","en":"Payment confirmation usually takes 1-2 minutes. If it is still not active after 5 minutes, open a ticket with your order number.","zh":"付款确认通常需要1-2分钟。超过5分钟仍未生效，请附上订单号提交工单。"}'),
		('billing', 20,
		 '{"ko":"환불할 수 있나요?","en":"Can I get a refund?","zh":"可以退款吗？"}',
		 '{"ko":"사용량이 거의 없는 경우 구매 후 일정 기간 내 환불을 검토합니다. 티켓에 주문 번호와 사유를 적어 주세요.","en":"If almost no data was used, we review refunds within a short period after purchase. Include your order number and the reason in a ticket.","zh":"若几乎未使用流量，购买后一定期限内可申请退款审核。请在工单中注明订单号与原因。"}'),
		('plans', 10,
		 '{"ko":"데이터가 갑자기 모두 소진됐어요.","en":"My data ran out suddenly.","zh":"流量突然用完了。"}',
		 '{"ko":"여러 기기에서 같은 구독을 쓰면 사용량이 합산됩니다. 내 기기 목록에서 모르는 기기가 없는지 확인해 보세요.","en":"Usage from every device on the same subscription adds up. Check your device list for any device you do not recognise.","zh":"同一订阅下所有设备的流量会累加。请检查设备列表中是否有不认识的设备。"}'),
		('plans', 20,
		 '{"ko":"속도가 느려요.","en":"The connection is slow.","zh":"速度很慢。"}',
		 '{"ko":"다른 서버 노드로 바꿔 보고, 클라이언트를 최신 버전으로 업데이트해 주세요. 계속 느리면 노드 이름과 시간대를 알려 주세요.","en":"Try another server node and update your client to the latest version. If it stays slow, tell us the node name and the time.","zh":"请尝试切换其他节点并将客户端更新到最新版本。若仍然很慢，请告知节点名称和时间。"}'),
		('clients', 10,
		 '{"ko":"구독을 가져오면 오류가 나요.","en":"Importing the subscription fails.","zh":"导入订阅时报错。"}',
		 '{"ko":"구독 링크가 정확히 복사됐는지, 플랜이 활성 상태인지 확인해 주세요. 서버 점검 중에는 잠시 후 다시 시도하면 됩니다.","en":"Check that the subscription link was copied completely and that your plan is active. During maintenance, try again shortly.","zh":"请确认订阅链接完整复制且套餐处于有效状态。维护期间请稍后重试。"}'),
		('apps', 10,
		 '{"ko":"사이트가 열리지 않아요.","en":"The website does not open.","zh":"网站打不开。"}',
		 '{"ko":"브라우저 캐시를 지우고 다시 접속해 보세요. 그래도 안 되면 사용 중인 네트워크와 오류 화면을 티켓에 첨부해 주세요.","en":"Clear your browser cache and try again. If it still fails, describe your network and the error in a ticket.","zh":"请清除浏览器缓存后重试。仍无法访问时，请在工单中说明网络环境和错误提示。"}'),
		('account', 10,
		 '{"ko":"비밀번호를 잊어버렸어요.","en":"I forgot my password.","zh":"忘记密码了。"}',
		 '{"ko":"로그인 화면의 비밀번호 재설정에서 가입한 이메일로 링크를 받을 수 있습니다.","en":"Use password reset on the sign-in page to get a link by email.","zh":"在登录页选择重置密码，即可通过邮件收到重置链接。"}'),
		('referral', 10,
		 '{"ko":"초대 코드는 어디서 확인하나요?","en":"Where do I find my invite code?","zh":"在哪里查看邀请码？"}',
		 '{"ko":"포털의 내 초대 메뉴에서 초대 링크와 현황을 볼 수 있습니다.","en":"Open My invites in the portal to see your link and its status.","zh":"在门户的「我的邀请」中可查看邀请链接与状态。"}')
	 ) AS f(slug, sort_order, questions, answers)
	 JOIN support_categories c ON c.slug = f.slug
	 WHERE EXISTS (SELECT 1 FROM marker)`,
}
