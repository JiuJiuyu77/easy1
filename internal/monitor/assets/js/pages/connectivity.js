async function loadConnectivityScopes(request){
  const data=await api('/api/connectivity/scopes',{signal:request?.signal});
  if(!isCurrentPageRequest(request))return null;
  S.connectivityScopes=data;
  const available=new Set((data.tags||[]).map(item=>item.tag));
  for(const tag of [...S.connectivitySelectedTags])if(!available.has(tag))S.connectivitySelectedTags.delete(tag);
  if(!S.connectivityTagsInitialized){
    for(const tag of available)S.connectivitySelectedTags.add(tag);
    S.connectivityTagsInitialized=true;
  }
  return data;
}

function connectivityTargetName(id){return {google:'Google',github:'GitHub',outlook:'Outlook',proxyspace:'ProxySpace'}[id]||id}
function verificationPolicyHTML(scope,test204,targets,disabled=false){
  const selected=targets instanceof Set?targets:new Set(targets||[]);
  const count=selected.size;
  const siteButtons=verificationTargets().map(target=>`<button type="button" class="site-selector ${selected.has(target.id)?'selected':''}" aria-pressed="${selected.has(target.id)?'true':'false'}" ${disabled?'disabled':''} onclick="toggleVerificationTarget('${scope}','${target.id}')"><strong>${target.name}</strong><span>${selected.has(target.id)?'必须成功':'不检测'}</span></button>`).join('');
  return `<div class="verification-policy"><div class="verification-policy-head"><div><strong>站点验收（可选）</strong><span>在上面探测目标通过后，再要求所选站点可达</span></div><span class="pill ${count?'green':'red'}">${count?`已选择 ${count} 项`:'未选择'}</span></div><div class="verification-targets">${siteButtons}</div><p class="muted">站点验收对限速/劫持线路容易误判，按需勾选即可。</p></div>`;
}
function verificationPolicy(scope){
  if(scope==='import')return {test_204:!!S.importTest204,site_targets:[...S.importSiteTargets]};
  return {test_204:S.subConfig?.test_204!==false,site_targets:Array.isArray(S.subConfig?.site_targets)?[...S.subConfig.site_targets]:[]};
}
function verificationPolicyValid(policy,showError=true){
  const valid=!!policy.test_204||(policy.site_targets||[]).length>0;
  if(!valid&&showError)openDialog('请选择检测方式','<p>204 测速和站点检测不能同时关闭。请至少启用一项后再继续。</p>');
  return valid;
}
function setVerification204(scope,enabled){
  if(scope==='import'){
    S.importTest204=!!enabled;
    localStorage.setItem(IMPORT_TEST_204_KEY,S.importTest204?'1':'0');
    renderImport();
    return;
  }
  S.subConfig={...(S.subConfig||{}),test_204:!!enabled};
  renderSettings();
}
function toggleVerificationTarget(scope,targetID){
  if(!verificationTargets().some(target=>target.id===targetID))return;
  if(scope==='import'){
    if(S.importSiteTargets.has(targetID))S.importSiteTargets.delete(targetID);else S.importSiteTargets.add(targetID);
    localStorage.setItem(IMPORT_SITE_TARGETS_KEY,JSON.stringify([...S.importSiteTargets]));
    renderImport();
    return;
  }
  const selected=new Set(S.subConfig?.site_targets||[]);
  if(selected.has(targetID))selected.delete(targetID);else selected.add(targetID);
  S.subConfig={...(S.subConfig||{}),site_targets:[...selected]};
  renderSettings();
}
function subscriptionConfigPayload(overrides={}){
  return {enabled:S.subConfig?.enabled!==false,interval:S.subConfig?.interval||'24h',subscriptions:S.subConfig?.subscriptions||[],...verificationPolicy('refresh'),...overrides};
}
function setConnectivityTimeout(value){
  const parsed=parseConnectivityTimeout(value);
  S.connectivityTimeoutInput=String(value??'');
  S.connectivityTimeoutValid=!parsed.error;
  if(!parsed.error){
    S.connectivityTimeoutSeconds=parsed.seconds;
    localStorage.setItem(CONNECTIVITY_TIMEOUT_KEY,String(parsed.seconds));
  }
  const input=qs('#connectivityTimeout');
  if(input){input.setAttribute('aria-invalid',parsed.error?'true':'false');input.setCustomValidity(parsed.error)}
  const error=qs('#connectivityTimeoutError');if(error)error.textContent=parsed.error;
  const start=qs('#connectivityStartButton');if(start)start.disabled=!!parsed.error||!(S.connectivityScopes?.tags||[]).length;
  return !parsed.error;
}
function connectivityPhaseText(job){
  return {first_pass:'首轮检测',retry_wait:'等待重试',retry:'失败项重试',saving_results:'正在保存结果',canceling:'正在终止',finished:'检测完成',failed:'检测失败',canceled:'已终止'}[job?.phase]||'等待检测';
}
function connectivityStatusText(status){return {running:'检测中',finished:'已完成',failed:'失败',canceled:'已终止'}[status]||'等待中'}
function renderConnectivity(){
  const scopes=S.connectivityScopes||{tags:[],targets:[]};
  const tagRows=(scopes.tags||[]).map((item,index)=>`<label class="tag-option"><input type="checkbox" ${S.connectivitySelectedTags.has(item.tag)?'checked':''} onchange="toggleConnectivityTagAt(${index},this.checked)"><span title="${esc(item.tag)}">${esc(item.tag)}</span><small class="muted">${esc(item.nodes)} 节点</small></label>`).join('');
  const running=S.connectivityJob?.status==='running';
  const timeoutValue=running?(Number(S.connectivityJob?.timeout_seconds)||CONNECTIVITY_DEFAULT_TIMEOUT_SECONDS):S.connectivityTimeoutInput;
  const sites=(scopes.targets||[]).map(target=>`<button class="site-selector ${S.connectivityTestTargets.has(target.id)?'selected':''}" ${running?'disabled':''} onclick="toggleConnectivityTestTarget('${esc(target.id)}')" title="${esc(target.url||'')}"><strong>${esc(target.name)}</strong><span>${S.connectivityTestTargets.has(target.id)?'本轮检测':'不检测'}</span></button>`).join('');
  qs('#view').innerHTML=`${customSitesHTML()}<div class="connectivity-layout">
    <section class="settings-section"><div class="settings-section-head connectivity-scope-head"><h2>检测范围</h2><div class="row"><button class="btn small secondary" onclick="setAllConnectivityTags(true)">全选</button><button class="btn small ghost" onclick="setAllConnectivityTags(false)">清空</button></div></div><div class="settings-section-body"><div class="connectivity-scope-summary"><span>已选 <strong id="connectivityTagCount">${S.connectivitySelectedTags.size}</strong> / ${esc((scopes.tags||[]).length)} 个 Tag</span><span>共 ${esc((scopes.tags||[]).reduce((sum,item)=>sum+(Number(item.nodes)||0),0))} 个节点</span></div><div class="tag-picker">${tagRows||'<div class="connectivity-idle" style="grid-column:1/-1"><span>暂无带 Tag 的节点</span></div>'}</div><div class="connectivity-scope-footer"><div class="connectivity-test-controls"><div class="site-strip">${sites}</div><div class="connectivity-timeout-field"><label for="connectivityTimeout">单次超时</label><div class="number-suffix"><input id="connectivityTimeout" type="number" min="1" max="60" step="1" value="${esc(timeoutValue)}" ${running?'disabled':''} aria-describedby="connectivityTimeoutError" aria-invalid="${S.connectivityTimeoutValid?'false':'true'}" oninput="setConnectivityTimeout(this.value)"><span>秒</span></div><small id="connectivityTimeoutError" class="field-error" aria-live="polite">${S.connectivityTimeoutValid?'':'请输入 1-60 秒的整数'}</small></div></div><div id="connectivityActions" class="row">${connectivityActionsHTML()}</div></div></div></section>
    <section id="connectivityProgressHost"></section>
    <section id="connectivityCriteria"></section>
    <section id="connectivityMatrixHost"></section>
    <section id="connectivityHistoryHost"></section>
    <section id="connectivityResults"></section>
  </div>`;
  updateConnectivityJobView(S.connectivityJob);
}

function connectivityActionsHTML(){
  const job=S.connectivityJob,running=job?.status==='running';
  if(running)return job.phase==='canceling'?'<button class="btn danger" disabled>正在终止</button>':'<button class="btn danger" onclick="cancelConnectivityJob()">终止检测</button>';
  const disabled=!(S.connectivityScopes?.tags||[]).length||!S.connectivityTimeoutValid?'disabled':'';
  return `<button class="btn primary" id="connectivityStartButton" ${disabled} onclick="startConnectivityJob()">开始检测</button>`;
}

function toggleConnectivityTagAt(index,checked){
  const tag=S.connectivityScopes?.tags?.[index]?.tag;if(typeof tag!=='string')return;
  if(checked)S.connectivitySelectedTags.add(tag);else S.connectivitySelectedTags.delete(tag);
  const count=qs('#connectivityTagCount');if(count)count.textContent=S.connectivitySelectedTags.size;
}
function setAllConnectivityTags(selected){
  S.connectivitySelectedTags.clear();
  if(selected)for(const item of S.connectivityScopes?.tags||[])S.connectivitySelectedTags.add(item.tag);
  renderConnectivity();
}

function toggleConnectivityTestTarget(id){
  if(S.connectivityTestTargets.has(id)){
    if(S.connectivityTestTargets.size===1){toast('至少选择一个检测站点','err');return}
    S.connectivityTestTargets.delete(id);
  }else S.connectivityTestTargets.add(id);
  renderConnectivity();
}

async function startConnectivityJob(){
  const tags=[...S.connectivitySelectedTags];
  const targets=[...S.connectivityTestTargets];
  if(!tags.length){toast('请至少选择一个 Tag','err');return}
  if(!targets.length){toast('请至少选择一个检测站点','err');return}
  const timeoutInput=qs('#connectivityTimeout');
  if(!setConnectivityTimeout(timeoutInput?.value??S.connectivityTimeoutInput)){timeoutInput?.focus();return}
  try{
    const timeout_seconds=S.connectivityTimeoutSeconds;
    const started=await api('/api/connectivity/jobs/start',{method:'POST',body:JSON.stringify({tags,targets,timeout_seconds})});
    S.connectivityJobID=started.job_id;S.connectivityJob={id:started.job_id,status:'running',phase:'first_pass',tags,targets,timeout_seconds,total_routes:0,total_checks:0,done_checks:0,retry_checks:0,retry_done:0,recovered:0};S.connectivityResultPage=null;S.connectivityHistory=null;S.connectivitySelectedTargets=new Set(targets);
    localStorage.setItem(CONNECTIVITY_JOB_KEY,started.job_id);
    renderConnectivity();
    await pollConnectivityJob(started.job_id,false);
  }catch(err){toast(err.message,'err')}
}

async function pollConnectivityJob(jobID,silent=false){
  if(!jobID||S.connectivityPollingJob===jobID)return;
  S.connectivityPollingJob=jobID;
  let last=null,failures=0;
  try{
    last=await api('/api/connectivity/jobs/status?id='+encodeURIComponent(jobID));
    if(last.targets?.length){
      S.connectivityTestTargets=new Set(last.targets);S.connectivitySelectedTargets=new Set(last.targets);
      const timeout=Number(last.timeout_seconds)||CONNECTIVITY_DEFAULT_TIMEOUT_SECONDS;
      S.connectivityTimeoutInput=String(timeout);S.connectivityTimeoutSeconds=timeout;S.connectivityTimeoutValid=true;localStorage.setItem(CONNECTIVITY_TIMEOUT_KEY,String(timeout));
      S.connectivityJob=last;if(S.page==='connectivity')renderConnectivity()
    }
    while(true){
      S.connectivityJob=last;updateConnectivityJobView(last);
      if(last.status!=='running')break;
      try{
        const pushed=await waitForJobEvent('connectivity',jobID,last.updated_at,30000);
        last=pushed||await api('/api/connectivity/jobs/status?id='+encodeURIComponent(jobID));
        failures=0;
      }catch(err){
        if(err.status===401||err.status===404)throw err;
        failures++;await new Promise(resolve=>setTimeout(resolve,Math.min(5000,500*Math.pow(2,Math.min(failures,3)))));
        last=await api('/api/connectivity/jobs/status?id='+encodeURIComponent(jobID));
      }
    }
    S.connectivityJob=last;updateConnectivityJobView(last);
    if(last.status==='finished'){
      await Promise.all([loadConnectivityResults(),loadConnectivityHistory()]);
      if(!silent)toast('站点检测完成');
    }else if(!silent){toast(last.status==='canceled'?'站点检测已终止':last.error||'站点检测失败','err')}
  }finally{
    S.connectivityPollingJob='';
    if(last&&last.status!=='running')forgetJobEvent('connectivity',jobID);
  }
}

function handleConnectivityPollError(err){
  S.connectivityPollingJob='';
  if(err.status===404){S.connectivityJobID='';localStorage.removeItem(CONNECTIVITY_JOB_KEY)}
  toast(err.status===404?'站点检测任务已失效':err.message,'err');
}

async function cancelConnectivityJob(){
  const id=S.connectivityJob?.id||S.connectivityJobID;if(!id)return;
  if(!confirm('确认终止当前站点检测？\n\n已经完成的结果会保留，但不能用于生成端口。'))return;
  try{S.connectivityJob=await api('/api/connectivity/jobs/cancel?id='+encodeURIComponent(id),{method:'POST'});updateConnectivityJobView(S.connectivityJob);toast('正在终止检测')}catch(err){toast(err.message,'err')}
}

function connectivityProgressHTML(job){
  if(!job)return '<div class="connectivity-idle"><span>尚未执行站点检测</span></div>';
  const timeout=Number(job.timeout_seconds)||CONNECTIVITY_DEFAULT_TIMEOUT_SECONDS;
  if(job.status!=='running')return `<div class="connectivity-progress"><div class="connectivity-progress-head"><div><strong>${esc(connectivityPhaseText(job))}</strong><div class="muted">已检测 ${esc(job.total_routes||0)} 条路由 · ${(job.targets||[]).map(connectivityTargetName).map(esc).join(' / ')} · 单次超时 ${esc(timeout)} 秒</div></div><span class="pill ${job.status==='finished'?'green':'red'}">${esc(connectivityStatusText(job.status))}</span></div>${job.error?`<div class="action-status err">${esc(job.error)}</div>`:''}${job.history_error?`<div class="action-status err">${esc(job.history_error)}</div>`:''}</div>`;
  const retry=job.phase==='retry'||job.phase==='retry_wait';
  const done=retry?(job.retry_done||0):(job.done_checks||0),total=retry?(job.retry_checks||0):(job.total_checks||0);
  const pct=total?Math.min(100,Math.round(done*100/total)):0;
  return `<div class="connectivity-progress"><div class="connectivity-progress-head"><div><strong>${esc(connectivityPhaseText(job))}</strong><div class="muted">路由 ${esc(job.total_routes||0)} · 站点 ${(job.targets||[]).map(connectivityTargetName).map(esc).join(' / ')} · 单次超时 ${esc(timeout)} 秒 · 首轮 ${esc(job.done_checks||0)}/${esc(job.total_checks||0)} · 重试 ${esc(job.retry_done||0)}/${esc(job.retry_checks||0)}</div></div><span class="pill amber">${esc(connectivityStatusText(job.status))}</span></div><div class="progress"><div style="width:${pct}%"></div></div>${job.error?`<div class="action-status err">${esc(job.error)}</div>`:''}</div>`;
}

function connectivityMatrixHTML(job){
  if(!job||!(job.summaries||[]).length)return '';
  const targets=job.targets||[];
  const columns=`grid-template-columns:minmax(150px,1fr) repeat(${targets.length},minmax(150px,1fr))`;
  const head=`<div class="connectivity-matrix-row connectivity-matrix-head" style="${columns}"><div>Tag</div>${targets.map(id=>`<div>${esc(connectivityTargetName(id))}</div>`).join('')}</div>`;
  const rows=(job.summaries||[]).map(summary=>{
    const byTarget=Object.fromEntries((summary.targets||[]).map(item=>[item.target_id,item]));
    const cells=targets.map(id=>{
      const item=byTarget[id]||{},total=Number(item.total)||0,passed=Number(item.passed)||0,partial=Number(item.partial)||0,rate=total?Math.round(passed*1000/total)/10:0;
      const cls=total&&passed===total?'good':partial?'partial':total&&passed===0?'bad':'';
      return `<div class="connectivity-cell ${cls}"><strong>${passed}/${total} · ${rate}%</strong><span>部分可用 ${partial} · 失败 ${item.failed||0}</span><span>首轮 ${item.first_passed||0} · 恢复 ${item.recovered||0} · 中位 ${item.median_latency_ms?esc(item.median_latency_ms)+' ms':'-'}</span></div>`;
    }).join('');
    return `<div class="connectivity-matrix-row" style="${columns}"><div><strong>${esc(summary.tag)}</strong><div class="muted">${esc(summary.routes||0)} 条路由</div></div>${cells}</div>`;
  }).join('');
  return `<section class="settings-section"><details><summary class="section-summary">成功率矩阵</summary><div class="settings-section-body"><div class="connectivity-matrix">${head}${rows}</div></div></details></section>`;
}

function updateConnectivityJobView(job){
  if(S.page!=='connectivity')return;
  const progress=qs('#connectivityProgressHost'),matrix=qs('#connectivityMatrixHost'),actions=qs('#connectivityActions');
  if(progress)progress.innerHTML=connectivityProgressHTML(job);
  if(matrix)matrix.innerHTML=connectivityMatrixHTML(job);
  if(actions)actions.innerHTML=connectivityActionsHTML();
  const running=job?.status==='running';qsa('.site-selector').forEach(button=>button.disabled=running);const timeout=qs('#connectivityTimeout');if(timeout)timeout.disabled=running;
  renderConnectivityHistory();
  renderConnectivityCriteria();
  renderConnectivityResults();
}

async function loadConnectivityHistory(){
  const jobID=S.connectivityJob?.id||S.connectivityJobID;if(!jobID)return;
  S.connectivityHistory=await api('/api/connectivity/history?id='+encodeURIComponent(jobID));
  renderConnectivityHistory();
}

function connectivityVerdictText(value){return {usable:'成功',partial:'部分可用',failed:'失败',missing:'无记录'}[value]||value||'-'}
function renderConnectivityHistory(){
  const host=qs('#connectivityHistoryHost');if(!host)return;
  const job=S.connectivityJob,history=S.connectivityHistory;
  if(!job||job.status!=='finished'){host.innerHTML='';return}
  if(!history){host.innerHTML='<div class="connectivity-idle"><span>正在读取历史对比...</span></div>';return}
  if(!history.available){host.innerHTML='<div class="connectivity-idle"><span>没有同范围历史结果，本轮已保存为后续对比基线。</span></div>';return}
  const c=history.overall||{};
  const metric=(label,value,cls='')=>`<div class="metric"><span>${label}</span><strong class="${cls}">${esc(value||0)}</strong></div>`;
  const targets=(history.targets||[]).map(item=>`<span class="pill">${esc(connectivityTargetName(item.target_id))}：继续成功 ${esc(item.continued_success||0)} / 新成功 ${esc(item.newly_successful||0)} / 新失败 ${esc(item.newly_failed||0)}</span>`).join('');
  const changes=(history.changes||[]).map(item=>`<div class="connectivity-change-row"><div><strong>${esc(item.node_name||item.route_fingerprint)}</strong><div class="muted">${(item.tags||[]).map(esc).join(' · ')}</div></div><span>${esc(connectivityVerdictText(item.previous))} → ${esc(connectivityVerdictText(item.current))}</span><span class="pill ${item.current==='usable'?'green':item.current==='partial'?'amber':item.current==='missing'?'':'red'}">${esc(connectivityVerdictText(item.current))}</span></div>`).join('');
  host.innerHTML=`<section class="settings-section"><details><summary class="section-summary">相比上一次同范围检测<span>新成功 ${esc(c.newly_successful||0)} · 新失败 ${esc(c.newly_failed||0)}</span></summary><div class="settings-section-body"><div class="connectivity-history-grid">${metric('继续成功',c.continued_success,'')}${metric('新成功',c.newly_successful,'good')}${metric('新失败',c.newly_failed,'')}${metric('继续未成功',c.continued_unsuccessful,'')}${metric('新增且无历史',c.no_history,'')}${metric('已移出范围',c.removed,'')}</div><div class="site-strip">${targets}</div>${changes?`<details><summary>查看状态发生变化的节点（${esc((history.changes||[]).length)}）</summary><div class="connectivity-change-list">${changes}</div></details>`:'<div class="muted">没有节点在成功、部分可用和失败之间发生变化。</div>'}</div></details></section>`;
}

function toggleConnectivityTarget(id){
  if(S.connectivitySelectedTargets.has(id)){if(S.connectivitySelectedTargets.size===1){toast('至少保留一个站点条件','err');return}S.connectivitySelectedTargets.delete(id)}else S.connectivitySelectedTargets.add(id);
  renderConnectivityCriteria();refreshConnectivityPreviewSummary();
}

function renderConnectivityCriteria(){
  const host=qs('#connectivityCriteria');if(!host)return;
  const job=S.connectivityJob;
  if(!job||job.status!=='finished'){host.innerHTML='';return}
  const buttons=(job.targets||[]).map(id=>`<button class="site-selector ${S.connectivitySelectedTargets.has(id)?'selected':''}" onclick="toggleConnectivityTarget('${id}')"><strong>${connectivityTargetName(id)}</strong><span>${S.connectivitySelectedTargets.has(id)?'必须成功':'不参与筛选'}</span></button>`).join('');
  host.innerHTML=`<section class="settings-section"><div class="settings-section-head"><h2>检测结论与端口应用</h2><button class="btn secondary" onclick="openConnectivityPortPreview()">预览并应用</button></div><div class="settings-section-body"><div class="site-strip">${buttons}</div><div id="connectivityPreviewSummary" class="settings-highlight"><span class="muted">正在计算检测结论...</span></div></div></section>`;
  refreshConnectivityPreviewSummary();
}

function connectivityPortRequest(allowEmpty=false){return {job_id:S.connectivityJob?.id||'',tags:S.connectivityJob?.tags||[],targets:[...S.connectivitySelectedTargets],allow_empty:allowEmpty}}
async function refreshConnectivityPreviewSummary(){
  const host=qs('#connectivityPreviewSummary');if(!host||S.connectivityJob?.status!=='finished')return;
  const request=connectivityPortRequest();
  try{
    const preview=await api('/api/connectivity/ports/preview',{method:'POST',body:JSON.stringify(request)});
    if(!qs('#connectivityPreviewSummary'))return;
    const total=Number(preview.qualifying||0)+Number(preview.non_qualifying||0);
    const metric=(label,value,cls='')=>`<div class="metric"><span>${label}</span><strong class="${cls}">${esc(value||0)}</strong></div>`;
    host.innerHTML=`<div class="result-metrics">${metric('检测总数',total)}${metric('成功',preview.qualifying,'good')}${metric('失败',preview.non_qualifying,'bad')}${metric('当前端口',preview.current_pool)}${metric('应用后端口',preview.projected_pool)}</div><span class="muted">确认应用后：新建 ${esc(preview.added||0)} 个端口，停止 ${esc(preview.removed||0)} 个端口，${esc(preview.will_fail||0)} 个节点进入失败区${preview.shared_retained?`，${esc(preview.shared_retained)} 个跨 Tag 节点继续保留`:''}。</span><span class="pill amber">尚未应用</span>`;
  }catch(err){host.innerHTML=`<span style="color:var(--red)">${esc(err.message)}</span>`}
}

async function openConnectivityPortPreview(){
  try{
    const request=connectivityPortRequest(),preview=await api('/api/connectivity/ports/preview',{method:'POST',body:JSON.stringify(request)});
    S.connectivityPreview=preview;S.connectivityPreviewRequest={...request,preview_token:preview.preview_token};
    const metric=(label,value,cls='')=>`<div class="metric"><span>${label}</span><strong class="${cls}">${esc(value)}</strong></div>`;
    const empty=preview.empty_blocked?'<div class="action-status err">没有节点满足条件。确认应用会清空所选 Tag 的非共享端口。</div>':'';
    const list=(title,items,total)=>!(items||[]).length?'':`<details><summary>${title}（共 ${esc(total||items.length)} 个，显示 ${esc(Math.min(items.length,10))} 个）</summary><div class="connectivity-change-list">${items.slice(0,10).map(item=>`<div class="connectivity-change-row"><div><strong>${esc(item.node_name||'-')}</strong><div class="muted">${(item.tags||[]).map(esc).join(' · ')}</div></div><span>${item.port?`当前端口 ${esc(item.port)}`:'尚无端口'}</span>${statePill(item.state)}</div>`).join('')}</div></details>`;
    const warning=preview.removed?`<div class="action-status err">应用后将停止 ${esc(preview.removed)} 个现有端口，请先检查明细。</div>`:'';
    const actions=`<button class="btn" type="button" onclick="closeDialog()">取消</button><button id="connectivityApplyButton" class="btn ${preview.removed||preview.empty_blocked?'danger':'primary'}" type="button" onclick="applyConnectivityPorts(${preview.empty_blocked?'true':'false'})">${preview.empty_blocked?'确认清空所选 Tag':'确认应用'}</button>`;
    openDialog('预览端口变更',`<div class="port-preview-grid">${metric('检测总数',(preview.qualifying||0)+(preview.non_qualifying||0))}${metric('成功',preview.qualifying,'good')}${metric('失败',preview.non_qualifying,'bad')}${metric('当前端口',preview.current_pool)}${metric('应用后端口',preview.projected_pool)}${metric('进入失败区',preview.will_fail)}</div><div class="settings-highlight"><strong>端口池尚未修改</strong><span class="muted">确认后才会应用；取消或关闭弹窗不会改变现有端口。</span></div>${warning}${empty}${list('新建端口示例',preview.added_items,preview.added)}${list('停止端口示例',preview.removed_items,preview.removed)}`,{wide:true,actions});
  }catch(err){toast(err.message,'err')}
}

async function applyConnectivityPorts(allowEmpty){
  const button=qs('#connectivityApplyButton'),oldText=button?.textContent;
  if(button){button.disabled=true;button.textContent='正在应用...'}
  try{
    const request={...(S.connectivityPreviewRequest||connectivityPortRequest()),allow_empty:!!allowEmpty};
    const result=await api('/api/connectivity/ports/apply',{method:'POST',body:JSON.stringify(request)});
    closeDialog();toast(result.build_failed?`端口已更新，当前池内 ${result.pool_count||0} 个节点；${result.build_failed} 个节点无法生成运行配置，已移入失败节点`:`端口已更新，当前池内 ${result.pool_count||0} 个节点`,result.build_failed?'err':'ok');
    S.connectivityPreview=null;S.connectivityPreviewRequest=null;
    await loadImportSummary();renderNav();refreshConnectivityPreviewSummary();
  }catch(err){toast(err.message,'err');if(button){button.disabled=false;button.textContent=oldText}}
}

async function loadConnectivityResults(){
  const jobID=S.connectivityJob?.id||S.connectivityJobID;if(!jobID)return;
  const f=S.connectivityResultFilters,params=new URLSearchParams({id:jobID,page:String(f.page||1),page_size:'100'});
  if(f.tag)params.set('tag',f.tag);if(f.target)params.set('target',f.target);if(f.status)params.set('status',f.status);
  S.connectivityResultPage=await api('/api/connectivity/results?'+params.toString());
  renderConnectivityResults();
}

function renderConnectivityResults(){
  const host=qs('#connectivityResults');if(!host)return;
  const job=S.connectivityJob,page=S.connectivityResultPage;
  if(!job||job.status!=='finished'){host.innerHTML='';return}
  const tags=(job.tags||[]).map(tag=>`<option value="${esc(tag)}" ${S.connectivityResultFilters.tag===tag?'selected':''}>${esc(tag)}</option>`).join('');
  const targetOptions=(job.targets||[]).map(id=>`<option value="${id}" ${S.connectivityResultFilters.target===id?'selected':''}>${connectivityTargetName(id)}</option>`).join('');
  const rows=(page?.items||[]).map(item=>{
    const verdict=item.verdict||(item.success?'usable':'failed');
    const badge=verdict==='usable'?'<span class="pill green">完整可用</span>':verdict==='partial'?'<span class="pill amber">部分可用</span>':'<span class="pill red">失败</span>';
    const http=[item.http_status||'',item.final_host||''].filter(Boolean).join(' · ')||item.failure_stage||'-';
    const components=(item.components||[]).map(component=>`${component.name||component.id}：${component.success?'成功':component.error||'失败'}`).join('；');
    const detail=components||item.error||'-';
    return `<div class="connectivity-detail-row"><div><strong>${esc(item.node_name||item.node_id)}</strong><div class="muted">${(item.tags||[]).map(tag=>esc(tag)).join(' · ')}</div></div><div>${esc(connectivityTargetName(item.target_id))}</div><div>${badge}</div><div>${item.latency_ms?esc(item.latency_ms)+' ms':'-'}</div><div title="${esc(item.content_type||'')}">${esc(http)}</div><div class="muted" title="${esc(detail)}">${esc(item.error||components||'-')}</div></div>`;
  }).join('');
  const total=page?.total||0,current=page?.page||1,pages=Math.max(1,Math.ceil(total/(page?.page_size||100)));
  host.innerHTML=`<section class="settings-section"><div class="settings-section-head"><h2>节点明细<span class="section-count"> · ${esc(total)} 条结果</span></h2><button class="btn small secondary" onclick="toggleConnectivityDetails()">${S.connectivityDetailsOpen?'收起明细':'查看明细'}</button></div>${S.connectivityDetailsOpen?`<div class="settings-section-body"><div class="row"><select style="width:170px" onchange="setConnectivityResultFilter('tag',this.value)"><option value="">全部 Tag</option>${tags}</select><select style="width:150px" onchange="setConnectivityResultFilter('target',this.value)"><option value="">全部站点</option>${targetOptions}</select><select style="width:140px" onchange="setConnectivityResultFilter('status',this.value)"><option value="">全部结果</option><option value="success" ${S.connectivityResultFilters.status==='success'?'selected':''}>完整可用</option><option value="partial" ${S.connectivityResultFilters.status==='partial'?'selected':''}>部分可用</option><option value="failed" ${S.connectivityResultFilters.status==='failed'?'selected':''}>失败</option></select></div><div class="connectivity-table"><div class="connectivity-detail-row connectivity-detail-head"><div>节点 / Tag</div><div>站点</div><div>结果</div><div>耗时</div><div>HTTP / 阶段</div><div>页面检查</div></div>${rows||'<div class="empty">没有符合筛选条件的结果</div>'}</div><div class="row" style="justify-content:flex-end"><button class="btn small" ${current<=1?'disabled':''} onclick="changeConnectivityResultPage(-1)">上一页</button><span class="muted">${current} / ${pages}</span><button class="btn small" ${current>=pages?'disabled':''} onclick="changeConnectivityResultPage(1)">下一页</button></div></div>`:''}</section>`;
}
function toggleConnectivityDetails(){S.connectivityDetailsOpen=!S.connectivityDetailsOpen;renderConnectivityResults()}

async function setConnectivityResultFilter(name,value){S.connectivityResultFilters[name]=value;S.connectivityResultFilters.page=1;await loadConnectivityResults()}
async function changeConnectivityResultPage(delta){S.connectivityResultFilters.page=Math.max(1,(S.connectivityResultFilters.page||1)+delta);await loadConnectivityResults()}

// 探测目标与判定方式：测速（手动/批量/导入验收）和自动检测共用这份配置。
function probeTargetsEditorHTML(scope){
  const mgmt=S.settings?.management||{};
  const targets=(Array.isArray(mgmt.probe_targets)&&mgmt.probe_targets.length)?mgmt.probe_targets:['https://www.gstatic.com/generate_204','https://cp.cloudflare.com/generate_204','https://www.github.com/'];
  const policy=mgmt.probe_policy==='all'?'all':'any';
  const field='probeTargets_'+scope, policyID='probePolicy_'+scope;
  return `<div class="probe-editor">
    <div class="row" style="gap:8px;align-items:center;flex-wrap:wrap"><strong>探测目标</strong><span class="muted">手动/批量测速与自动检测都使用这里的目标，返回 2xx 即算通过</span></div>
    <textarea id="${field}" rows="3" placeholder="每行一个，可填任意 http(s) URL 或 host:port">${esc(targets.join('\n'))}</textarea>
    <div class="row" style="gap:8px;align-items:center;flex-wrap:wrap">
      <select id="${policyID}" style="max-width:220px">
        <option value="any" ${policy==='any'?'selected':''}>任一通过即算可用</option>
        <option value="all" ${policy==='all'?'selected':''}>全部通过才算可用</option>
      </select>
      <button class="btn small secondary" type="button" onclick="saveProbeTargets('${scope}')">保存探测配置</button>
    </div></div>`;
}

// 探测配置入口：导入节点 / 订阅刷新 / 基础运行三处共用同一个组件，
// 保持样式与位置一致；编辑器仍然只有顶栏「探测配置」弹窗一处。
function probeConfigSummaryHTML(disabled=false){
  const mgmt=(S.settings&&S.settings.management)||{};
  const targets=(Array.isArray(mgmt.probe_targets)&&mgmt.probe_targets.length)?mgmt.probe_targets:['https://www.gstatic.com/generate_204'];
  const policy=mgmt.probe_policy==='all'?'全部通过才算可用':'任一通过即算可用';
  return `<div class="probe-card">
    <div class="probe-card-main">
      <div class="probe-card-title"><strong>探测目标</strong><span class="pill green">${targets.length} 个</span><span class="pill">${policy}</span></div>
      <p class="muted probe-card-list">${esc(targets.join(' · '))}</p>
    </div>
    <button class="btn small secondary" type="button" ${disabled?'disabled':''} onclick="openProbeConfig()">编辑</button>
  </div>`;
}

// 全局入口：任何页面都能打开探测配置（目标 + 判定方式）。
function openProbeConfig(){
  openDialog('探测配置', `<p class="muted">探测、手动测速、自动测速（导入验收/订阅刷新）共用这份配置；每行一个目标，返回 2xx 即算通过。</p>${probeTargetsEditorHTML('global')}`);
}
async function saveProbeTargets(scope){
  const targets=(qs('#probeTargets_'+scope)?.value||'').split('\n').map(v=>v.trim()).filter(Boolean);
  if(!targets.length){toast('至少填写一个探测目标','err');return}
  const policy=qs('#probePolicy_'+scope)?.value||'any';
  try{
    await api('/api/settings',{method:'PUT',body:JSON.stringify({probe_targets:targets,probe_policy:policy})});
    toast('探测配置已保存，下一次测速/探测立即生效');
    qs('#resultModal')?.classList.remove('show');
    await refresh();
  }catch(err){toast(err.message,'err')}
}

// 站点按钮 = 内置 + 运维自定义
function verificationTargets(){
  const custom=((S.settings&&S.settings.verification&&S.settings.verification.custom_sites)||[]).map(site=>({id:site.id,name:site.name||site.id,custom:true}));
  return [...VERIFICATION_TARGETS,...custom];
}
function customSitesHTML(){
  const sites=(S.settings?.verification?.custom_sites)||[];
  const rows=sites.length?sites.map(site=>`<div class="table-row" style="grid-template-columns:1fr 1.6fr 1fr auto"><span>${esc(site.name||site.id)}</span><span class="muted">${esc(site.url)}</span><span class="muted">${esc(site.marker||'-')}</span><span class="row-actions"><button class="btn small danger" type="button" onclick="removeCustomSite('${esc(site.id)}')">删除</button></span></div>`).join(''):'<div class="empty">还没有自定义站点</div>';
  return `<div class="card"><div class="card-head"><div><h2>自定义站点</h2><p class="muted">保存后会出现在「站点检测」和「节点可用条件」里；marker 是响应体必须包含的关键字，可留空（只校验 2xx）。</p></div></div>
    <div class="card-body stack">${rows}
      <div class="settings-form">
        <div class="field"><label>名称</label><input id="customSiteName" placeholder="例如 我的站点"></div>
        <div class="field"><label>URL</label><input id="customSiteURL" placeholder="https://example.com/"></div>
        <div class="field"><label>marker（可选）</label><input id="customSiteMarker" placeholder="响应需包含的关键字"></div>
      </div>
      <div class="settings-actions"><button class="btn secondary" type="button" onclick="addCustomSite()">添加自定义站点</button></div>
    </div></div>`;
}
async function saveCustomSites(list){
  await api('/api/settings',{method:'PUT',body:JSON.stringify({verification:{custom_sites:list}})});
  toast('自定义站点已保存');
  await loadSettings();
  await refresh();
}
function addCustomSite(){
  const name=(qs('#customSiteName')?.value||'').trim();
  const url=(qs('#customSiteURL')?.value||'').trim();
  const marker=(qs('#customSiteMarker')?.value||'').trim();
  if(!url){toast('请填写站点 URL','err');return}
  const id=(name||url).toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-+|-+$/g,'').slice(0,24);
  const list=[...((S.settings?.verification?.custom_sites)||[])];
  const finalID=id||('site'+Date.now().toString(36));
  if(list.some(site=>site.id===finalID)){toast('该站点已存在','err');return}
  list.push({id:finalID,name:name||finalID,url,marker});
  saveCustomSites(list).catch(err=>toast(err.message,'err'));
}
function removeCustomSite(id){
  const list=((S.settings?.verification?.custom_sites)||[]).filter(site=>site.id!==id);
  saveCustomSites(list).catch(err=>toast(err.message,'err'));
}
