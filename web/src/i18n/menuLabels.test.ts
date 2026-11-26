import { describe, expect, it } from 'vitest'
import ko from './locales/ko.json'
import en from './locales/en.json'

const at = (tree: unknown, key: string): unknown =>
  key.split('.').reduce<unknown>((n, p) => (n && typeof n === 'object' ? (n as Record<string, unknown>)[p] : undefined), tree)

// One Korean name per menu, tab and action, the same wherever it appears.
const KO: Record<string, string> = {
  'layout.nav.cron': '예약 작업', 'cluster.nav.security': '키 교체',
  'firewall.tabs.rules': '방화벽 규칙', 'firewall.tabs.ports': '열린 포트', 'firewall.tabs.fail2ban': '접속 차단',
  'firewall.tabs.docker': '컨테이너 포트 규칙', 'packages.systemUpdates': '패키지 업데이트',
  'cluster.overview.rollingUpdate': '패널 순차 업데이트', 'cluster.overview.simultaneousUpdate': '패널 동시 업데이트',
  'appStore.updateAvailable': '새 버전 있음', 'docker.images.checkUpdates': '새 이미지 확인',
  'settings.update': '패널 업데이트', 'running.empty.appStore': '앱스토어',
  'layout.nav.system': '시스템', 'layout.nav.tasks': '그룹 작업', 'cluster.nav.nodes': '서버 목록',
  'layout.tabs.summary': '요약', 'layout.tabs.runningHere': '실행 중인 항목',
  'layout.groups.fleet': '모든 서버', 'layout.server.attached': '접속 중', 'layout.server.offline': '{{count}}대 꺼짐',
  'layout.scope.server': '서버 {{name}}', 'layout.scope.fleet': '모든 서버 · {{total}}대 중 {{online}}대 온라인',
  'layout.scope.savedFleet': '모든 서버 공통', 'layout.scope.savedServer': '{{name}}에만 저장됩니다', 'layout.scope.notFound': '찾을 수 없는 서버',
  'layout.mobileNav.menu': '메뉴',
  'layout.nav.map': '구성도', 'map.tabs.devices': '기기', 'map.tabs.rack': '랙',
  'settings.alerts.ruleType.lanNewDevice': '구성도: 새 기기',
  'settings.tabBrowser': '언어·테마', 'settings.tabSystem': '업데이트·백업', 'settings.tabHttps': 'HTTPS 인증서',
  'layout.scope.savedBrowser': '이 브라우저에만 저장됩니다',
  'layout.nav.tuning': '성능 튜닝',
  'layout.tabs.setup': '구성',
  'cluster.tasks.fleet.onMany': '여러 서버에서…', 'cluster.tasks.fleet.runSnippet': '여러 서버에서 실행',
  'cluster.tasks.run.toTerminal': '이 서버 터미널', 'cluster.tasks.run.toServer': '이 서버 열기',
  'cluster.addAgent.waySsh': 'SSH로 설치', 'cluster.addAgent.wayToken': '가입 명령 복사(토큰)',
  'terminal.openHere': '여기서 터미널 열기',
}
// A page's <h1> is its menu label.
const H1: [string, string][] = [
  ['files.title', 'layout.nav.files'], ['logs.title', 'layout.nav.logs'], ['processes.title', 'layout.nav.processes'],
  ['packages.title', 'layout.nav.packages'], ['disk.title', 'layout.nav.disk'], ['cron.title', 'layout.nav.cron'],
  ['appStore.title', 'layout.nav.appstore'], ['services.title', 'layout.nav.services'], ['network.title', 'layout.nav.networkVpn'],
  ['firewall.title', 'layout.nav.firewall'], ['docker.title', 'layout.nav.docker'], ['running.title', 'layout.nav.running'],
  ['cluster.title', 'layout.nav.cluster'], ['settings.title', 'layout.nav.settings'], ['map.title', 'layout.nav.map'],
  ['tuning.title', 'layout.nav.tuning'],
]

describe('menu labels', () => {
  it.each(Object.entries(KO))('%s reads %s in Korean', (key, value) => expect(at(ko, key)).toBe(value))
  it.each([['ko', ko], ['en', en]] as const)('every page heading is its menu label in %s', (_lang, tree) => {
    expect(H1.filter(([h1, nav]) => at(tree, h1) !== at(tree, nav))).toEqual([])
  })
})
