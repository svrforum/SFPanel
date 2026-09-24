import { test, expect, type BrowserContext } from '@playwright/test'

async function fixture(context: BrowserContext) {
 await context.addInitScript(()=>{sessionStorage.setItem('token','docker-fixture'); localStorage.setItem('sfpanel_language','en')})
 const state={holdAlpha:false,releaseAlpha:null as (() => void) | null,pruned:false,requests:[] as {path:string;method:string;body:string|null}[],errors:[] as string[]};
 const projects=['alpha','beta'].map(name=>({name,compose_file:'compose.yaml',path:'/opt/stacks/'+name,has_env:true,service_count:1,running_count:1,real_status:'running'}));
 const cs=[{Id:'bad-api',Names:['/unhealthy-api'],Image:'nginx:latest',State:'running',Status:'Up 1 hour (unhealthy)',Ports:[{PrivatePort:80,PublicPort:8080,Type:'tcp'}],Created:1750000000,Labels:{'com.docker.compose.project':'alpha','com.docker.compose.service':'app'}}, {Id:'stopped-worker',Names:['/stopped-worker'],Image:'alpine:3.20',State:'exited',Status:'Exited (0) 1 hour ago',Ports:[],Created:1750000000,Labels:{}}];
 await context.routeWebSocket(/\/ws\//, ws=>{});
 await context.route('**/api/v1/**',async route=>{
  const req=route.request(), url=new URL(req.url()),p=url.pathname.replace('/api/v1','');
  state.requests.push({path:p,method:req.method(),body:req.postData()});
  let data: unknown={};
  if(p==='/auth/setup-status') data={setup_required:false};
  else if(p==='/auth/ws-ticket') data={ticket:'review'};
  else if(p==='/cluster/status') data={enabled:false};
  else if(p==='/cluster/nodes') data=[];
  else if(p==='/system/overview') data={version:'review'};
  else if(p==='/docker/compose') data=projects;
  else if(/^\/docker\/compose\/(alpha|beta)$/.test(p)) {
    const name=p.split('/').at(-1);
    if(name==='alpha' && state.holdAlpha) await new Promise<void>(r=>state.releaseAlpha=r);
    data={project:projects.find(x=>x.name===name),yaml:`services:\n  ${name}:\n    image: ${name}:latest\n`};
  }
  else if(p.endsWith('/services')) data=[{name:'app',container_id:'bad-api',image:'nginx:latest',state:'running',status:'Up 1 hour (unhealthy)',ports:'8080:80'}];
  else if(p.endsWith('/env')) data={content:'EXAMPLE=true'};
  else if(p.endsWith('/diff')) data={summary:{added:0,modified:0,removed:0},by_category:{}};
  else if(p.endsWith('/up-stream')) { await route.fulfill({contentType:'text/event-stream',body:'data: {"phase":"complete","line":"Deployed"}\n\n'}); return }
  else if(p.endsWith('/validate')) data={valid:!req.postData()?.includes('INVALID'),message:'Invalid editor YAML'};
  else if(p.endsWith('/rollback')) data={has_rollback:false};
  else if(p==='/docker/containers') data=cs;
  else if(p.includes('/stats/batch')||p.endsWith('/metrics')||p.endsWith('/events')) data=[];
  else if(p==='/docker/images') data=state.pruned?[]:[{Id:'sha256:1234567890abcdef',RepoTags:['nginx:latest','nginx:stable'],Size:100000000,Created:1750000000,in_use:true,used_by:['unhealthy-api']}];
  else if(p==='/docker/volumes') data=[{Name:'database-data',Driver:'local',Mountpoint:'/var/lib/docker/volumes/database-data/_data',CreatedAt:'2026-09-01T00:00:00Z',in_use:true,used_by:['unhealthy-api'],size_bytes:900000000}];
  else if(p==='/docker/networks') data=[{Id:'network-id',Name:'alpha_default',Driver:'bridge',Scope:'local',in_use:true,used_by:['unhealthy-api']}];
  else if(p.includes('/docker/prune/')) {state.pruned=true;data={deleted:1,space_reclaimed:1000}}
  else if(p.includes('check-updates')) data=[];
  else if(p.includes('/inspect')) data={id:'network-id',name:'alpha_default',driver:'bridge',scope:'local',subnet:'172.18.0.0/16',gateway:'172.18.0.1',containers:[{id:'bad-api',name:'unhealthy-api',ipv4_address:'172.18.0.2',mac_address:'02:42:ac:12:00:02'}]};
  await route.fulfill({json:{success:true,data}});
 });
 return state
}

test('stack requests cannot overwrite another stack; validation sends the draft', async ({ page, context }) => {
 const state = await fixture(context)
 state.holdAlpha = true
 await page.goto('/docker/stacks/alpha')
 await expect.poll(() => !!state.releaseAlpha).toBe(true)
 await page.getByRole('button', {name: /^beta/}).first().click()
 await page.getByRole('tab', {name:'Editor', exact:true}).click()
 await expect(page.locator('.view-lines')).toContainText('beta:')
 state.holdAlpha = false
 state.releaseAlpha?.()
 await expect(page.locator('.view-lines')).toContainText('beta:')
 await page.locator('.monaco-editor').click()
 await page.keyboard.press('Control+a')
 await page.keyboard.insertText('services: INVALID')
 await page.getByRole('button',{name:'Validate',exact:true}).click()
 await expect(page.getByText('Invalid editor YAML',{exact:true})).toBeVisible()
 expect(JSON.parse(state.requests.filter(r => r.path.endsWith('/validate')).at(-1)!.body!).yaml).toContain('INVALID')
 await page.getByRole('button',{name:/^alpha/}).first().click()
 await page.getByRole('button',{name:/^beta/}).first().click()
 await expect(page.locator('.view-lines')).toContainText('INVALID')
 expect(state.requests.filter(r => r.method === 'PUT')).toHaveLength(0)
})

test('stop preserves containers; teardown requires a separate confirmation', async ({ page, context }) => {
 const state = await fixture(context)
 await page.goto('/docker/stacks/alpha')
 await page.getByRole('button',{name:'Stop',exact:true}).last().click()
 await expect.poll(() => state.requests.some(r => r.path.endsWith('/alpha/stop'))).toBe(true)
 expect(state.requests.some(r => r.path.endsWith('/down'))).toBe(false)
 await page.getByRole('button',{name:'Remove containers (Down)',exact:true}).click()
 await expect(page.getByRole('alertdialog').or(page.getByRole('dialog')).last()).toContainText('Volumes and Compose files are preserved')
 expect(state.requests.some(r => r.path.endsWith('/down'))).toBe(false)
})

for (const width of [390, 1440]) {
 test(`Docker resources stay usable at ${width}px`, async ({ page, context }) => {
  await fixture(context)
  await page.setViewportSize({width,height:1000})
  for (const menu of ['stacks','containers','images','volumes','networks']) {
   await page.goto(`/docker/${menu}`)
   await expect(page.getByRole('navigation', {name:'Docker'}).getByRole('link',{name:'Networks'})).toBeVisible()
   if(menu === 'containers') {
    await expect(page.locator('main').getByText('Unhealthy',{exact:true}).filter({visible:true}).first()).toBeVisible()
    await page.getByRole('button',{name:/Batch/}).click()
    await expect(page.getByRole('checkbox').first()).toBeVisible()
   }
   if(menu === 'images') {
    await expect(page.getByRole('button',{name:'Pull Image',exact:true})).toBeVisible()
    await expect(page.locator('main').getByText(/nginx:stable/).filter({visible:true}).first()).toBeVisible()
   }
   if(['images','volumes','networks'].includes(menu)) {
    await expect(page.locator('main').getByRole('link',{name:'unhealthy-api'}).filter({visible:true}).first()).toBeVisible()
    await page.getByRole('textbox',{name:'Search names, tags or containers'}).fill('no-match')
    await expect(page.getByText('No resources match these filters.')).toBeVisible()
   }
   expect(await page.locator('main').evaluate(el=>el.scrollWidth-el.clientWidth)).toBeLessThanOrEqual(1)
  }
 })
}

test('global cleanup refreshes the active list and shows per-resource results', async ({ page, context }) => {
 const state = await fixture(context)
 await page.goto('/docker/images')
 await expect(page.locator('main').getByText(/nginx:latest/).filter({visible:true}).first()).toBeVisible()
 await page.getByRole('button',{name:'Clean up all',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:/Prune/}).click()
 await page.getByRole('dialog').last().getByRole('button',{name:'Delete',exact:true}).click()
 await expect.poll(() => state.pruned).toBe(true)
 await expect(page.getByRole('dialog')).toContainText('Images: 1')
 await expect(page.locator('main').getByText(/nginx:latest/)).toHaveCount(0)
})

test('network endpoints link to containers and offer connect/disconnect', async ({page,context}) => {
 const state = await fixture(context)
 await page.goto('/docker/networks')
 await page.getByRole('button',{name:'Inspect',exact:true}).last().click()
 const dialog = page.getByRole('dialog')
 await expect(dialog.getByRole('link',{name:'unhealthy-api'})).toHaveAttribute('href','/docker/containers?container=bad-api')
 await dialog.getByRole('combobox',{name:'Connect container'}).selectOption('stopped-worker')
 await dialog.getByRole('button',{name:'Connect container'}).click()
 await expect.poll(() => state.requests.some(r=>r.path === '/docker/networks/network-id/connect' && r.body?.includes('stopped-worker'))).toBe(true)
 await dialog.getByRole('button',{name:'Disconnect',exact:true}).click()
 await expect(page.getByRole('dialog').last()).toContainText('may interrupt')
})

test('mobile shell sends control keys, resizes, and reconnects explicitly', async ({page,context}) => {
 await fixture(context)
 await page.setViewportSize({width:390,height:850})
 const messages: string[] = []
 let sockets = 0
 let disconnect: (() => void) | undefined
 await context.routeWebSocket(/\/ws\/docker\/containers\/.*\/exec/, ws => {
  sockets++
  disconnect = () => ws.close()
  ws.onMessage(message => messages.push(String(message)))
 })
 await page.goto('/docker/containers')
 await page.getByRole('button',{name:'Terminal',exact:true}).filter({visible:true}).first().click()
 await expect(page.getByRole('button',{name:'Ctrl+C',exact:true})).toBeEnabled()
 await page.getByRole('button',{name:'Ctrl+C',exact:true}).click()
 await expect.poll(()=>messages.includes('\x03')).toBe(true)
 await page.getByRole('button',{name:'Tab',exact:true}).click()
 await expect.poll(()=>messages.includes('\t')).toBe(true)
 await page.getByRole('button',{name:'Expand',exact:true}).click()
 await expect(page.locator('[data-shell-expanded="true"]')).toBeVisible()
 expect(await page.locator('[data-shell-expanded="true"]').evaluate(el=>el.getBoundingClientRect().height)).toBeGreaterThan(700)
 disconnect?.()
 await expect(page.getByRole('button',{name:'Reconnect',exact:true})).toBeVisible()
 const count = sockets
 await page.waitForTimeout(200)
 expect(sockets).toBe(count)
 await page.getByRole('button',{name:'Reconnect',exact:true}).click()
 await expect.poll(()=>sockets).toBe(count+1)
})

test('failed refresh retains the last successful resource list', async ({page,context}) => {
 await fixture(context)
 await page.goto('/docker/images')
 await expect(page.getByText(/nginx:stable/).filter({visible:true})).toBeVisible()
 await page.route('**/api/v1/docker/images', route=>route.fulfill({status:503,json:{success:false,error:{message:'Docker unavailable'}}}))
 await page.getByRole('button',{name:'Refresh',exact:true}).click()
 await expect(page.getByText(/nginx:stable/).filter({visible:true})).toBeVisible()
 await expect(page.getByText(/Docker unavailable/).first()).toBeVisible()
 await expect(page.getByRole('button',{name:'Retry',exact:true}).first()).toBeVisible()
})


test('deployment previews unchanged configuration, then validates before saving and starting', async ({page,context}) => {
 const state = await fixture(context)
 await page.goto('/docker/stacks/beta')
 await page.getByRole('tab',{name:'Editor',exact:true}).click()
 await expect(page.locator('.view-lines')).toContainText('beta:')
 await page.getByRole('button',{name:'Deploy',exact:true}).click()
 await expect(page.getByRole('dialog')).toContainText('No changes')
 expect(state.requests.some(r=>r.method === 'PUT')).toBe(false)
 await page.getByRole('dialog').getByRole('button',{name:/Apply/}).click()
 await expect.poll(()=>state.requests.some(r=>r.path.endsWith('/up-stream'))).toBe(true)
 const validate = state.requests.findIndex(r=>r.path.endsWith('/validate'))
 const save = state.requests.findIndex(r=>r.method === 'PUT')
 const deploy = state.requests.findIndex(r=>r.path.endsWith('/up-stream'))
 expect(validate).toBeGreaterThan(-1)
 expect(save).toBeGreaterThan(validate)
 expect(deploy).toBeGreaterThan(save)
})
