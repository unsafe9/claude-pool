# 멀티플랫폼 지원 설계 (Windows / Linux / WSL 확장)

claude-pool을 macOS 전용에서 **Linux · WSL · Windows(Git Bash & 순수 PowerShell)**까지
확장하기 위한 기획. 구현 전 합의용. 부수 효과로 Linux 지원도 대부분 따라온다.

## 1. 목표 / 범위 (확정 결정)

- **목표 환경**: macOS, Linux, **WSL**, **Windows + Git Bash**, **Windows + 순수
  PowerShell**(Git Bash 없는 네이티브) — 전부 1차 지원.
- **hook 진입(확정)**: hooks.json을 **exec form**(`command`+`args`)으로 두고
  `claude-pool` 바이너리를 직접 호출한다. exec form은 셸 비경유(실측)라 Git Bash·
  PowerShell·sh 무관하게 동작 → **PowerShell 1차 지원**. hook 로직(이벤트 분기·
  self-update·exit remap)은 전부 Go 바이너리에 내장.
- **단일 command(확정)**: Windows exec form이 확장자 없는 이름을 `.exe`로 자동
  resolve함을 실측 확인 → **단일 `command: "claude-pool"`이 전 OS에서 동작**.
  hooks.json OS 분기 불필요(애초에 미지원).
- **부트스트랩(확정, B2)**: `claude-pool`을 **PATH(`~/.local/bin`)에 설치** +
  바이너리 self-update. gobin/GOPATH 탐색 레거시는 제거. 최초 설치만 OS별 인스톨러
  1회.
- **릴리즈(확정)**: **goreleaser**로 darwin/linux/windows 전 플랫폼·arch 바이너리를
  한 번에 빌드·게시. 자산 명명이 self-update·인스톨러의 contract.
- **WSL(확정)**: WSL은 **Linux로 취급**(배포판 내 cc 관리). Windows↔WSL interop은 비목표.
- **플랫폼 분리(확정)**: `cgo` 없이 **빌드 태그**로 플랫폼별 파일 분리.

## 2. 핵심 통찰: 4개 환경 = 2축

셸에 민감한 건 hook 진입점뿐이고, exec form이 그 민감도를 제거한다. 바이너리는
GOOS로만 갈린다.

| 실행 환경 | 바이너리 | credential 저장소 | hook 진입 | 상태 |
|-----------|----------|------------------|-----------|------|
| macOS | darwin | Keychain (`security`) | exec form(셸 무관) | 기존 |
| Linux | linux | `~/.claude/.credentials.json` | exec form | 평문 백엔드로 흡수 |
| WSL (배포판 내 cc) | linux | `~/.claude/.credentials.json` (WSL fs) | exec form | = Linux |
| Windows + Git Bash | windows(.exe) | `%USERPROFILE%\.claude\.credentials.json` | exec form | 실측 ✅ |
| Windows + 순수 PowerShell | windows(.exe) | 동일 | exec form(셸 비경유) | **exec form으로 해결 ✅** |

미묘한 주의: Windows에서 hook이 부르는 건 **Windows 바이너리(.exe)**다(cc가 Windows
claude.exe → Windows 경로 credential). 바이너리의 `os.UserHomeDir()`이
`C:\Users\<user>`로 정확히 떨어진다.

## 3. 확정된 cc 동작 (실측 근거; cc 2.1.173, Windows 11)

- **자격증명**: macOS=Keychain, Linux/WSL/Windows=`~/.claude/.credentials.json` 평문.
  blob=`{"claudeAiOauth":{accessToken,refreshToken,expiresAt,scopes,subscriptionType,
  rateLimitTier}}` — macOS 풀 blob과 동일 포맷. Windows는 Credential Manager/DPAPI
  미사용(cmdkey 비어 있음). 파일 ACL은 `.claude` 디렉토리((OI)(CI)) 상속.
- **경로**: 전 OS `~/.claude/` 동일(`os.UserHomeDir()`).
- **hook 실행 모델(핵심)**:
  - **shell form**(`command` 문자열만): Windows에서 Git Bash `bash.exe -c`로 감쌈
    (Git Bash 없으면 PowerShell). 조상체인 `claude.exe → bash.exe -c → 스크립트`.
  - **exec form**(`command`+`args`): **셸 비경유 직접 CreateProcess**. 조상체인
    `claude.exe → <command> 프로세스` (bash 안 낌). 실측 확정.
  - **확장자 resolve**: exec form `command`가 절대경로든 PATH bare name이든, 확장자
    없는 `claude-pool`을 Windows에서 `claude-pool.exe`로 자동 resolve. 실측 확정.
  - hooks.json **OS 분기 필드 없음**(문서 확인) → 단일 command 필요(위 resolve로 해결).
- **apiKeyHelper**: settings.json 필드라 exec form 불가(shell form 고정). 기존 형식
  `'<경로>' helper`(작은따옴표 sh 인용)는 Windows에서 깨짐(exited 1, filename syntax).
  → Windows 전용 형식 필요(OQ-Helper).

## 4. 잔여 사항 (웹 조사로 확정; 구현 후 1회 실측 확인)

- **apiKeyHelper 실행(확정·조사)**: Windows에서 apiKeyHelper는 **cmd.exe로 실행**된다
  (Node `execa` `shell:true` → `ComSpec`=cmd.exe). hook(Git Bash/PowerShell 분기)과
  다른 코드 경로. 근거: 우리 에러 `filename syntax incorrect`=cmd.exe 에러, hooks
  문서만 Windows 셸 분기를 명시(apiKeyHelper 문서는 `/bin/sh`만), GH anthropics/
  claude-code #11639에서 `pwsh -NoProfile -Command "& '...'"` 형식 동작 보고.
  → **형식: 작은따옴표(shQuote) 금지**(cmd.exe서 리터럴), **큰따옴표 경로
  `"<exe>" helper`**(공백 대비; cmd.exe는 `/`·`\` 둘 다 허용). Anthropic 미공식이라
  구현 후 1회 실측 확인. (GH #13013 경로확장 이슈는 절대경로라 무관)
- **self-update 자기교체(확정·조사)**: Windows 실행 중 `.exe`는 **rename 가능**
  (delete 불가; Go #21997, `go build`도 동일 트릭). 패턴: 같은 디렉토리 temp 작성 →
  자기를 `.old`로 rename(AV 일시잠금 대비 retry 백오프) → temp를 원래 경로로 rename →
  `.old`는 hide(SetFileAttributes HIDDEN), 다음 실행 시 `os.Remove`. `os.Rename`은
  Windows에서 MoveFileEx(REPLACE_EXISTING). `MOVEFILE_DELAY_UNTIL_REBOOT`는 admin
  필요라 미사용. 라이브러리 불필요(~50줄, `x/sys/windows`). "다음 세션 활성화" 모델과 정합.
- **WSL2(미확정)**: flock·평문 파일 정상 동작은 실환경 1회 확인(E-linux). 위험 낮음.
- (해소) ~~OQ-PS~~: exec form 셸 비경유 + 확장자 resolve 실측으로 PowerShell 확정.

## 5. 아키텍처: 빌드 태그 분리

cgo 없이 플랫폼 의존 함수만 파일 단위 분리(시그니처 공통, 구현 분기).

```
internal/pool/
  cred_store.go          // ReadCredential/WriteCredential 공통 진입점
  cred_store_darwin.go   // security CLI                 //go:build darwin
  cred_store_other.go    // 평문 .credentials.json        //go:build !darwin
  filelock_unix.go       // flock                        //go:build !windows
  filelock_windows.go    // LockFileEx                   //go:build windows
  proc_unix.go           // Signal(0), Setsid            //go:build !windows
  proc_windows.go        // OpenProcess, DETACHED_PROCESS //go:build windows
cmd/claude-pool/
  exec_unix.go           // execClaude = syscall.Exec    //go:build !windows
  exec_windows.go        // execClaude = Run()+os.Exit   //go:build windows
  helper_unix.go         // helperCommand (sh 인용)       //go:build !windows
  helper_windows.go      // helperCommand (Windows 인용)  //go:build windows
  hook.go                // `claude-pool hook <event>` + self-update (OS 무관)
```

`golang.org/x/sys` 이미 의존 → `x/sys/windows`로 LockFileEx/OpenProcess/CreateProcess
플래그 직접 호출(추가 의존성 없음).

## 6. 변경 명세 (파일별)

- **6.1 자격증명** `keychain.go`→`cred_store*.go` [상]: 공통 진입점 뒤로 추상화.
  `_darwin`=security CLI 그대로, `_other`(linux+windows+wsl)=`~/.claude/.credentials.json`
  read/write(temp+rename atomic; **Windows는 `os.Chmod` 금지** — `.claude` ACL 상속;
  chmod는 `_unix`만). blob 동일 포맷이라 import/저장/write-back 재사용. cc 동시 refresh
  경합은 기존 `harvest()` 화해로 커버. `ErrUnsupportedOS` 가드 제거.
- **6.2 파일락** `store.go` [중]: `lockFile/unlockFile` 분리. `_unix`=flock,
  `_windows`=LockFileEx/UnlockFileEx.
- **6.3 PID 생존** `process.go` [하]: `_unix`=Signal(0)+EPERM, `_windows`=OpenProcess+
  GetExitCodeProcess(STILL_ACTIVE).
- **6.4 detached** `main.go` [중]: recovery waker의 `/bin/sh -c "sleep N"`→`claude-pool
  __wake <sec>` 서브커맨드(Go). startDetached: `_unix`=Setsid, `_windows`=
  CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS.
- **6.5 execClaude** `main.go` [하]: `_unix`=syscall.Exec, `_windows`=Run()+os.Exit.
- **6.6 apiKeyHelper** `main.go` [중]: `helperCommand`를 `_unix`/`_windows`로 분리.
  `_unix`=현 shQuote(작은따옴표). `_windows`=**cmd.exe 인용** `"<exe>" helper`(큰따옴표;
  작은따옴표는 cmd.exe서 리터럴이라 깨짐 — 조사 확정). `isOurHelper` suffix 매칭은
  OS 무관 유지.

## 7. 부트스트랩 & install

### 7.1 hooks.json — exec form 단일 command
```json
{ "type": "command", "command": "claude-pool", "args": ["hook", "session-start"] }
```
- exec form → 셸 비경유, 전 OS·전 셸 동작. `claude-pool`은 PATH에서 resolve
  (Windows는 `.exe` 자동). OS 분기 불필요.
- 이벤트별 `hook session-start` / `hook background` / `hook stop-failure`.

### 7.2 `hook <event>` 서브커맨드 (`hook.go`)
- session-start: 빈 풀이면 `import` → `auto --if-needed --threshold 0.9`. 자기 version
  vs plugin.json 대조 → 다르고 ≠`dev`면 백그라운드 self-update. + `__wake` 서브커맨드
  + recovery waker를 sh→`__wake`로 전환.
- background: detached `auto --if-needed`. stop-failure: `auto`.
- **exit 2 절대 금지**(hook 경로는 0/1만).
- **self-update**: `net/http`로 F2 자산(`claude-pool-<os>-<arch>[.exe]`) 다운로드 후
  교체. macOS/Linux=temp+rename. **Windows 자기교체**(조사 확정): 같은 디렉토리 temp →
  자기를 `.old`로 rename(AV retry) → temp를 원래 경로로 rename → `.old` hide; session-start
  진입 시 leftover `.old` 정리. curl/sed/nohup 의존 제거.
- **gobin 레거시 제거**: 기존 `find_pool`의 GOBIN/GOPATH/`~/go/bin` 탐색 폐기 →
  `~/.local/bin` 표준(cc 자신이 거기 거주, 이미 PATH).

### 7.3 최초 설치 (인스톨러)
- 바이너리가 없으면 exec form hook은 조용히 실패(비치명적). 최초 1회만 설치 필요.
- OS별 인스톨러 원라이너: posix `curl -fsSL .../install.sh | sh`, PowerShell
  `irm .../install.ps1 | iex`. `~/.local/bin`에 OS/arch 자산 설치 + PATH 보장 안내.
- 이후 버전 갱신은 self-update가 담당.
- **(후속 변경, v0.2.1)** 최초 설치도 자동화: SessionStart에 부트스트랩 훅 2개 추가
  (exec form `sh` → unix 전용 / `powershell.exe` → Windows 전용; 없는 OS에서는
  "명령 미존재 → 조용한 실패"로 자기선택). 각 훅은 바이너리 부재 시 동봉 인스톨러를
  백그라운드 실행(다음 세션 활성). 원라이너는 즉시/수동 설치용으로 유지.
- **(후속 변경, v0.2.2)** v0.2.1의 "조용한 실패" 가정은 오류로 판명 — exec form
  spawn 실패(`Executable not found in $PATH`)는 cc가 **항상 사용자에게 표시**
  (cc 2.1.173 바이너리에서 확인, 억제 필드 없음). 즉 모든 플랫폼에서 매 세션
  에러 1줄. 부트스트랩 훅 2개를 **string-form 훅 1개**로 교체: string form은
  unix에선 `/bin/sh`, Windows에선 cc가 직접 해석한 Git Bash로 실행되어
  (`CLAUDE_CODE_GIT_BASH_PATH` → `Program Files` 표준 경로 → PATH의 git.exe에서
  유도; PATH의 sh와 무관) macOS/Linux/WSL/Windows+Git Bash 전부 무음+동작.
  bootstrap.sh가 `uname` MINGW 분기로 `powershell.exe -File install.ps1`을
  백그라운드 실행, bootstrap.ps1 삭제. 트레이드오프: Git Bash 없는 순수
  PowerShell 환경은 자동 부트스트랩 상실(훅이 "requires bash" 에러 1줄/세션,
  수동 원라이너로 1회 설치; exec form 메인 훅들은 그대로 동작).

> 대안 B1(plugin `bin/` 동봉): `${CLAUDE_PLUGIN_ROOT}/bin/claude-pool`(확장자 없이)도
> 실측상 Windows에서 .exe resolve되어 단일 command 가능. 완전 자동(최초도)이나 멀티-OS
> 바이너리를 소스 repo에 커밋해야 해 비대 + goreleaser 산출물과 중복 → 미채택.

## 8. 릴리즈: goreleaser

`.goreleaser.yaml` + GitHub Actions(tag `v*`). 기존 `release.yml`(darwin 단일) 대체.
- builds: `CGO_ENABLED=0`, GOOS=darwin/linux/windows × GOARCH=amd64/arm64
  (darwin/amd64 선택), ldflags `-s -w -X main.version={{.Version}}`.
- **자산 명명 = F2 contract**: self-update·인스톨러와 일치하도록 고정
  (`claude-pool-<os>-<arch>[.exe]`). windows는 `.exe`.
- release: GitHub Release 첨부 + 체크섬.

## 9. Task Breakdown (병렬 wave)

**Objective**: 5개 환경 동작 + goreleaser 전 플랫폼 릴리즈 + PATH 설치/self-update.
**Done**: macOS/Linux/WSL/Git Bash/PowerShell e2e 통과, goreleaser 자산 게시,
hooks.json exec form 단일 command로 동작.

**Contracts (직렬 사슬을 끊는 핵심)**
- **C1**=플랫폼 추상화 시그니처(F1) — 모든 `_windows`/`_other` 구현 게이트.
- **C2**=자산 명명/다운로드 URL 규칙(F2) — goreleaser·self-update·인스톨러 공유.

### Wave 0 — Foundation (병렬 2)
- **F1 · 플랫폼 추상화 골격**: 빌드 태그 파일 구조 + 공통 진입점 시그니처. darwin 구현을
  `_darwin`/`_unix`로 이동, `store.go`/`main.go` 호출부를 추상 함수로 치환, windows 스텁.
  **`main.go` 서브커맨드 디스패치·OS의존 호출부를 여기서 정리**(Wave 1의 `main.go` 편집은
  T-hook 단독). Verify: darwin `go build && go test ./...` 무회귀 + `GOOS=windows
  go build ./...` 컴파일. Creates **C1**.
- **F2 · 자산/URL contract**: `claude-pool-<os>-<arch>[.exe]` 명명 + 다운로드 base를
  Go 상수+문서로 고정. Verify: 상수에서 자산명 생성 단위테스트. Creates **C2**.

### Wave 1 — 플랫폼 구현 + 릴리즈 (병렬 8, 전부 disjoint 신규 파일)
- **T-cred** `cred_store_other.go` 평문 백엔드(chmod는 `_unix`만). Verify: windows/linux
  빌드 + temp dir read/write/none 단위테스트. Needs C1.
- **T-lock** `filelock_windows.go`(LockFileEx) + `_unix` 마감. Needs C1.
- **T-pid** `proc_windows.go` pidAlive. Needs C1.
- **T-detach** `proc_windows.go` startDetached(DETACHED_PROCESS)만. (`main.go` 비편집;
  `__wake`/waker는 T-hook) Needs C1.
- **T-exec** `exec_windows.go` execClaude. Needs C1.
- **T-helper** `helper_windows.go` helperCommand=`"<exe>" helper`(cmd.exe 큰따옴표,
  조사 확정). Needs C1.
- **T-hook** `hook.go`: `hook <event>` + self-update + `__wake` + waker 전환 + gobin
  레거시 제거. (`main.go` switch/`scheduleRecoveryWake`는 **T-hook 단독 소유**)
  Verify: macOS hooks.json을 exec form hook으로 바꿔 3 이벤트 동작 동일 + waker sh 없이
  재실행. Needs C1, C2.
- **T-rel** `.goreleaser.yaml` + workflow(release.yml 대체). Verify: `goreleaser release
  --snapshot --clean`이 전 플랫폼 자산+체크섬 생성, 자산명 C2 일치. Needs C2.

### Wave 2 — 배선 + 인스톨러 (병렬 2)
- **T-wire** `hooks.json`(exec form 단일 command)·`plugin.json`·`claude-pool-run.sh`
  제거. Verify: macOS에서 SessionStart→`hook session-start` 직접 동작. Needs T-hook.
- **T-install** OS별 인스톨러 `install.sh`+`install.ps1`(OS/arch 감지→`~/.local/bin`
  설치). Verify: 각 스크립트가 자산명 정확 매핑 + 설치 후 `claude-pool version`. Needs C2.
  (T-wire와 disjoint: hooks.json vs install 스크립트)

### Wave 3 — 검증 (별도 환경; 서로 병렬)
- **C-Helper** `human-verify`(Windows): 구현된 cmd.exe `"<exe>" helper` 형식이 실제
  cc apikey 모드에서 동작하는지 1회 확인(즉시종료 진단키로 cc 행 방지).
- **E-linux** `human-verify`: Linux/WSL2 import→auto→swap(평문+flock; OQ-WSL2).
- **E-win** `human-verify`: Windows+Git Bash e2e + self-update 자기교체(OQ-SelfUpdate).
  v0.2.2 추가 확인: string-form 부트스트랩 훅이 Git Bash로 무음 실행되는지
  (`${CLAUDE_PLUGIN_ROOT}` 슬래시 정규화 치환, cc env의 bash에서 `nohup`/`uname`
  가용 여부, nohup된 powershell.exe 인스톨러가 훅 종료 후 생존하는지).
- **E-ps** `human-verify`: 순수 PowerShell(Git Bash 없는 Windows) e2e — exec form
  메인 훅이라 위험 낮으나 실환경 1회 확인. v0.2.2부터 자동 부트스트랩 없음:
  수동 원라이너 설치 후, string-form 훅의 "requires bash" 에러가 1줄/세션으로
  비치명적인지 확인.

### Waves 한눈에
- **Wave 0**(병렬): F1, F2
- **Wave 1**(병렬, C1/C2 후): T-cred, T-lock, T-pid, T-detach, T-exec, T-helper,
  T-hook, T-rel
- **Wave 2**(병렬): T-wire, T-install
- **Wave 3**(병렬, 별도 환경): C-Helper, E-linux, E-win, E-ps

**Critical path**: F1 → T-hook → T-wire → E-win (≈4단계). Wave 1이 가장 넓음(8).
**Next unblocked**: F1, F2.
**Decision checkpoints**: F1 직후 seam 재검토; C-Helper가 T-helper 최종 확정.
**Deferred**: WSL↔Windows interop(비목표); B1 bin/ 동봉(미채택, 7.3).

## 10. 리스크 / 트레이드오프

- **apiKeyHelper(조사 확정, 미공식)**: cmd.exe 실행 + 큰따옴표 형식으로 확정했으나
  Anthropic 공식 문서엔 없음 → 구현 후 C-Helper 1회 실측. exec form 못 쓰는 유일한
  진입점(settings 필드).
- **self-update(조사 확정)**: rename-self + `.old` hide + AV retry. 라이브러리 불필요.
- **최초 설치 1회(B2)**: 완전 무인 설치는 포기(exec form 제약상 셸 부트스트랩 불가).
  대가는 인스톨러 원라이너 1회이며 이후 self-update 자동. repo 청결.
- **평문 자격증명(Linux/Windows)**: cc 자신이 그렇게 저장하므로 보안 동일(악화 아님).
- **CLAUDE.md "Releasing" 절 교체**: darwin 단일 `release.yml` 전제 → goreleaser 전
  플랫폼 + PATH 설치/self-update로 문서 갱신(T-rel/T-install과 함께).
