# 접속 도메인 추가

AWS `18.232.151.51`의 저장소 루트에서 실행한다. Ubuntu의 Python 3.10 이상과 Docker CLI만 필요하다. 저장소 설치 위치로 이동한 뒤 도메인만 바꿔 한 명령으로 추가한다.

```bash
python3 scripts/add-access-domain.py m.example.com --origin-ip 18.232.151.51
```

토큰 환경변수가 없으면 대화형 터미널에서 화면에 표시하지 않고 입력받는다. 파일이나 설정에 토큰을 저장하지 않는다. 비대화형 실행에서는 토큰이 없으면 변경 전에 실패한다.

## 최초 준비

- Cloudflare에서 대상 zone에만 접근 가능한 사용자 API 토큰을 만든다.
- 권한은 **Zone > Zone > Read**, **Zone > DNS > Edit**, **Zone > Config Rules > Edit**가 필요하다. API 권한명은 `Config Settings Write`로 표시될 수도 있다.
- 해당 계정/요금제에서 `ssl` Configuration Rule을 지원해야 한다.
- 실행 계정은 Docker 실행 권한과 실제 Caddyfile 및 같은 디렉터리의 백업 생성 권한이 필요하다.
- `proxima-vpn-caddy-1`이 실행 중이어야 한다. `/etc/caddy/Caddyfile` 또는 `/etc/caddy`의 호스트 마운트가 저장소 안에 있어야 한다. 기존 `(panel)` snippet은 수정하지 않는다.
- 도메인은 해당 Cloudflare zone의 DNS를 사용해야 하며 원본의 80/443 포트가 접근 가능해야 한다. 인증서 확인을 위해 이 호스트에서 원본 공인 IP의 443에도 접속 가능해야 한다.

여러 번 실행할 때는 셸 기록에 토큰을 남기지 않고 현재 셸에만 설정할 수 있다.

```bash
read -rsp 'Cloudflare API token: ' CLOUDFLARE_API_TOKEN; printf '\n'
export CLOUDFLARE_API_TOKEN
export PROXIMA_ORIGIN_IP=18.232.151.51
python3 scripts/add-access-domain.py p.example.com
unset CLOUDFLARE_API_TOKEN
```

`--origin-ip` > `PROXIMA_ORIGIN_IP` > EC2 IMDSv2 `public-ipv4` 순서로 원본 IP를 결정한다. IMDSv2는 프록시 없이 조회하며 사용 불가하면 명시적으로 IP를 지정한다.

## 옵션과 안전장치

```bash
# Cloudflare 프록시 없이 DNS만 연결 (Caddy 구성과 HTTPS 확인도 수행)
python3 scripts/add-access-domain.py go.example.com --origin-ip 18.232.151.51 --dns-only

# zone 탐색 생략. 값은 실제 32자리 zone ID로 대체
python3 scripts/add-access-domain.py m.example.com --zone-id YOUR_ZONE_ID

# 스크립트 위치에서 계산한 저장소 루트와 실제 설치 루트가 다를 때
python3 scripts/add-access-domain.py m.example.com --project-dir /opt/proxima-vpn
```

자동 zone 탐색은 호스트명에서 왼쪽 label을 하나씩 제거하며 접근 가능한 zone을 찾는다. `--zone-id`도 도메인이 그 zone에 속하는지 확인한다.

정확히 같은 이름의 A 레코드만 Auto TTL로 생성한다. 기본은 proxied이고 `--dns-only`는 프록시를 끈다. 기존 레코드가 IP·타입·프록시·TTL 중 하나라도 다르면 덮어쓰지 않고 중단한다. 운영자가 확인하고 기존 레코드를 먼저 정리해야 한다. 다른 이름의 레코드는 변경하지 않는다.

## 적용 순서와 Flexible 문제

1. 현재 마운트와 Caddyfile을 읽고 같은 inode에 배타 잠금을 건다.
2. `<도메인> { import panel }` 블록만 추가한 후보를 실행 중 컨테이너의 Caddy로 검증한다. 아직 활성 파일이나 Cloudflare는 바꾸지 않는다.
3. DNS 충돌과 기존 Configuration Rules를 확인한 뒤 필요한 A 레코드만 생성한다.
4. 원본 파일의 백업을 만들고 기존 inode에 후보를 쓴 다음 `caddy reload`로 적용한다. 컨테이너·API·DB를 재시작하지 않는다.
5. 원본 IP에 도메인 SNI로 TLS 연결하여 공개 신뢰 인증서 발급을 확인한다. 프록시 도메인은 이 확인이 끝난 다음에만 `http.host eq "해당 도메인"`의 `set_config`, `ssl: strict` 규칙을 추가한다. 기존 strict 규칙 뒤에 다른 활성 SSL 규칙이 있으면 중복 생성하지 않고 그 호스트의 strict 규칙만 마지막으로 이동한다. DNS 전용 모드는 인증서만 기다리며 SSL 규칙을 만들지 않는다. zone 전체 SSL 모드와 다른 규칙의 내용·상대 순서는 바꾸지 않는다.
6. 실제 `https://도메인/health`가 유효한 인증서로 200을 반환하는지 확인한다. 리다이렉트는 따라가지 않는다. 308도 성공이 아니다.

Flexible은 방문자와 Cloudflare 사이만 HTTPS이고 원본에는 HTTP로 접속한다. Caddy가 이를 HTTPS로 돌리면 같은 HTTPS URL로 계속 308을 반환할 수 있다. 호스트 한정 strict 규칙은 Cloudflare와 원본 사이도 HTTPS와 인증서 검증을 사용하게 한다. **먼저 원본 인증서를 발급받고 strict를 활성화**하므로 HTTP 인증 요청이 아직 인증서가 없는 strict 원본을 거치는 상황을 피한다.

원본 인증서 확인은 최대 41회, 최종 HTTPS 확인은 최대 12회이며 접속당 timeout은 5초, 재시도 간격은 3초다. 원본 인증서는 즉시 연결이 거절돼도 2분 동안 기다려 Caddy의 1분 후 발급 재시도를 수용한다. 발급이나 DNS 반영이 더 늦으면 실패할 수 있으며 원인을 확인한 뒤 같은 명령을 재실행한다. 이미 일치하는 레코드·호스트 블록·활성 strict 규칙은 중복 추가하지 않는다.

## 백업과 실패 복구

백업은 Caddyfile 옆 `Caddyfile.backup-*`로 생성하고 경로를 출력한다. reload 실패 시 이전 내용을 **동일 inode에** 복원하고 다시 reload한다. 복원 reload까지 실패하면 백업 경로와 오류를 출력한다.

후속 인증서/규칙/HTTPS 확인 실패 시 이미 추가한 DNS, 규칙, 적용된 Caddy 구성은 남는다. 출력에 시도한 원격 쓰기 API 목록이 나오며, 네트워크 오류라면 해당 요청이 서버에 반영됐는지는 Cloudflare에서 확인해야 한다. 자동으로 DNS를 삭제하거나 zone 규칙을 되돌리지 않는다. 오류를 해결하고 재실행하는 것을 우선한다.

수동 Caddy 복원이 필요한 경우 출력된 백업의 내용을 `cp 백업경로 실제Caddyfile경로`로 기존 파일에 복사한 뒤 아래를 실행한다. bind mount가 끊어지므로 `mv`로 파일을 교체하지 않는다. 백업 후 다른 작업자가 변경했다면 먼저 그 변경을 확인한다.

```bash
docker exec proxima-vpn-caddy-1 caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

코드 검증(실제 Cloudflare/Docker 접근 없음):

```bash
python3 -m unittest discover -s tests/access_domains -v
```
