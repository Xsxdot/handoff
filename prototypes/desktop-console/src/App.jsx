import { useMemo, useState } from 'react';
import {
  Activity,
  AlertCircle,
  AtSign,
  BadgeCheck,
  Bell,
  Bot,
  Boxes,
  Cable,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  Clock3,
  Columns2,
  Command,
  Copy,
  Cpu,
  Eye,
  EyeOff,
  FileCode2,
  FilePenLine,
  FileText,
  Folder,
  FolderOpen,
  Folders,
  GitBranch,
  Globe2,
  HardDrive,
  House,
  KeyRound,
  LayoutDashboard,
  Link2,
  ListFilter,
  LockKeyhole,
  Monitor,
  MoreHorizontal,
  Network,
  PanelBottomClose,
  PanelBottomOpen,
  PanelRight,
  Play,
  Plus,
  Power,
  RefreshCw,
  Save,
  Search,
  SearchCode,
  Send,
  Server,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  SquareTerminal,
  TriangleAlert,
  Trash2,
  UserCheck,
  UserRound,
  Variable,
  Wifi,
  WifiOff,
  Wrench,
  X,
} from 'lucide-react';

const projectRows = [
  { id: 'super-debug', name: 'super-debug', color: '#9b5de5', directories: 2, running: 2, attention: 1, host: 'devbox-01' },
  { id: 'ai-hub', name: 'ai-hub', color: '#4aa3df', directories: 2, running: 1, attention: 0 },
  { id: 'school', name: 'school', color: '#44b678', directories: 1, running: 1, attention: 0 },
  { id: 'openrouter', name: 'openrouter', color: '#619df0', directories: 1, running: 0, attention: 0 },
  { id: 'sq', name: 'sq', color: '#171717', directories: 1, running: 1, attention: 1 },
];

const boardColumns = [
  {
    id: 'todo',
    title: '等待执行',
    tone: 'neutral',
    tasks: [
      { id: 'T-018', title: 'Leader election + e2e', project: 'super-debug', directory: 'integration/b2-b3', machine: 'devbox-01', executor: 'OpenCode', age: '刚刚', state: '已排队' },
      { id: 'T-021', title: '整理 provider 错误码', project: 'ai-hub', directory: 'main', machine: 'devbox-01', executor: 'Codex', age: '12m', state: '等待执行' },
    ],
  },
  {
    id: 'running',
    title: '进行中',
    tone: 'online',
    tasks: [
      { id: 'T-004', title: '修复 DropPeer 并发测试', project: 'super-debug', directory: 'integration/b2-b3', machine: 'devbox-01', executor: 'OpenCode', age: '23m', state: '运行测试', progress: 72, taskKey: 'running' },
      { id: 'T-009', title: '补齐图像 MIME 校验', project: 'ai-hub', directory: 'fix/media-mime', machine: 'ci-runner-02', executor: 'Codex', age: '41m', state: '编辑代码', progress: 48 },
      { id: 'T-013', title: '刷新课程章节结构', project: 'school', directory: 'main', machine: 'devbox-01', executor: 'Claude Code', age: '1h', state: '读取文件', progress: 31 },
    ],
  },
  {
    id: 'review',
    title: 'Review',
    tone: 'attention',
    tasks: [
      { id: 'T-005', title: '更新 DropPeer 测试快照', project: 'super-debug', directory: 'integration/b2-b3', machine: 'devbox-01', executor: 'Codex', age: '2m', state: '等待审批', attention: true, taskKey: 'approval' },
      { id: 'T-015', title: '检查远端安装脚本', project: 'sq', directory: 'v2-batch4-snapshot', machine: 'devbox-01', executor: 'OpenCode', age: '18m', state: '等待 Review' },
    ],
  },
  {
    id: 'done',
    title: '完成',
    tone: 'done',
    tasks: [
      { id: 'T-003', title: 'Persist drops & metrics', project: 'super-debug', directory: 'integration/b2-b3', machine: 'devbox-01', executor: 'OpenCode', age: '3h', state: '已完成' },
      { id: 'T-011', title: 'Console.md', project: 'openrouter', directory: 'main', machine: 'devbox-01', executor: 'Codex', age: '昨天', state: '已完成' },
    ],
  },
];

const codeByFile = {
  'transport_test.go': [
    ['68', 'func TestDropPeerIdempotent(t *testing.T) {'],
    ['69', '    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)'],
    ['70', '    defer cancel()'],
    ['71', ''],
    ['72', '    ids := make([]uint64, 5)'],
    ['73', '    for i := range ids {'],
    ['74', '        ids[i] = uint64(i + 1)'],
    ['75', '    }'],
    ['76', ''],
    ['77', '    // 并发发起多次 DropPeer，请求应为幂等'],
    ['78', '    var wg sync.WaitGroup'],
    ['79', '    start := make(chan struct{})'],
    ['80', '    errs := make(chan error, 20)'],
    ['81', ''],
    ['82', '    for i := 0; i < 20; i++ {'],
    ['83', '        wg.Add(1)'],
    ['84', '        go func() {'],
    ['85', '            defer wg.Done()'],
    ['86', '            <-start'],
    ['87', '            for _, id := range ids {'],
    ['88', '                if err := tr.DropPeer(id); err != nil &&'],
    ['89', '                    !errors.Is(err, ErrPeerNotFound) {'],
    ['90', '                    errs <- fmt.Errorf("drop %d: %w", id, err)'],
    ['91', '                }'],
    ['92', '            }'],
    ['93', '        }()'],
    ['94', '    }'],
    ['95', ''],
    ['96', '    close(start) // 同时启动'],
    ['97', '    wg.Wait()'],
    ['98', '    close(errs)'],
    ['99', ''],
    ['100', '    for err := range errs {'],
    ['101', '        if err != nil {'],
    ['102', '            t.Fatal(err)'],
    ['103', '        }'],
    ['104', '    }'],
    ['105', '}'],
  ],
  'transport.go': [
    ['74', 'func (tr *transport) DropPeer(id uint64) error {'],
    ['75', '    tr.mu.Lock()'],
    ['76', '    defer tr.mu.Unlock()'],
    ['77', ''],
    ['78', '    peer, ok := tr.peers[id]'],
    ['79', '    if !ok {'],
    ['80', '        return ErrPeerNotFound'],
    ['81', '    }'],
    ['82', ''],
    ['83', '    delete(tr.peers, id)'],
    ['84', '    return peer.Close()'],
    ['85', '}'],
  ],
  'group.go': [
    ['18', 'type group struct {'],
    ['19', '    mu    sync.RWMutex'],
    ['20', '    peers map[uint64]*peer'],
    ['21', '}'],
    ['22', ''],
    ['23', 'func (g *group) Len() int {'],
    ['24', '    g.mu.RLock()'],
    ['25', '    defer g.mu.RUnlock()'],
    ['26', '    return len(g.peers)'],
    ['27', '}'],
  ],
};

const fileRows = [
  { name: 'internal', type: 'folder', depth: 0 },
  { name: 'cluster', type: 'folder', depth: 1 },
  { name: 'cluster_test.go', type: 'file', depth: 2 },
  { name: 'group.go', type: 'file', depth: 2 },
  { name: 'transport.go', type: 'file', depth: 2 },
  { name: 'transport_test.go', type: 'file', depth: 2, modified: true },
  { name: 'config', type: 'folder', depth: 0 },
  { name: 'core', type: 'folder', depth: 0 },
  { name: 'metrics', type: 'folder', depth: 0 },
  { name: 'replication', type: 'folder', depth: 0 },
  { name: 'rpc', type: 'folder', depth: 0 },
  { name: 'store', type: 'folder', depth: 0 },
  { name: 'sysinfo', type: 'folder', depth: 0 },
  { name: 'test', type: 'folder', depth: 0 },
  { name: 'web', type: 'folder', depth: 0 },
  { name: 'go.mod', type: 'file', depth: 0 },
  { name: 'README.md', type: 'file', depth: 0 },
  { name: 'Makefile', type: 'file', depth: 0, modified: true },
];

function IconButton({ label, children, className = '', ...props }) {
  return (
    <button className={`icon-button ${className}`} type="button" aria-label={label} title={label} {...props}>
      {children}
    </button>
  );
}

function SummaryCounts({ directories, running, attention, machine = false }) {
  return (
    <span className="summary-counts" aria-label={`${directories} 个目录，${running} 个运行任务${machine ? '' : `，${attention} 个待处理`}`}>
      <span title="开发目录"><Folders size={13} />{directories}</span>
      <span title="运行中的 handoff"><Activity size={13} />{running}</span>
      {!machine && <span title="需要处理"><TriangleAlert size={13} />{attention}</span>}
    </span>
  );
}

function StatusDot({ tone = 'online' }) {
  return <span className={`status-dot ${tone}`} aria-hidden="true" />;
}

function ProjectRow({ project, expanded, onToggle }) {
  return (
    <button className="tree-row project-row" type="button" onClick={onToggle}>
      <span className="row-leading">
        {project.id === 'super-debug' ? (expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : <span className="chevron-space" />}
        <Boxes size={16} color={project.color} strokeWidth={2.3} />
        <strong>{project.name}</strong>
      </span>
      <SummaryCounts {...project} />
    </button>
  );
}

function MachineRow({ name, connected = true, expanded, directories, running, onToggle }) {
  return (
    <button className={`tree-row machine-row ${connected ? '' : 'disabled'}`} type="button" onClick={connected ? onToggle : undefined} disabled={!connected}>
      <span className="row-leading">
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        {connected ? <Monitor size={14} /> : <WifiOff size={14} />}
        <StatusDot tone={connected ? 'online' : 'offline'} />
        <span>{name}</span>
        {!connected && <small>已断开</small>}
      </span>
      <SummaryCounts directories={directories} running={running} attention={0} machine />
    </button>
  );
}

function DirectoryRow({ label, detail, icon: Icon, selected, connected = true, onClick }) {
  return (
    <button className={`tree-row directory-row ${selected ? 'selected' : ''} ${connected ? '' : 'disabled'}`} type="button" onClick={connected ? onClick : undefined} disabled={!connected}>
      <span className="row-leading">
        {selected ? <ChevronDown size={13} /> : <span className="chevron-space" />}
        <Icon size={14} />
        <strong>{label}</strong>
        {detail && <small>{detail}</small>}
      </span>
      {selected && <StatusDot />}
    </button>
  );
}

function TaskRow({ label, executor, age, tone, active, onClick }) {
  return (
    <button className={`task-row ${active ? 'active' : ''}`} type="button" onClick={onClick}>
      <StatusDot tone={tone} />
      <span className="task-copy">
        <span>{label}</span>
        <small>· {executor}</small>
      </span>
      <time>{age}</time>
    </button>
  );
}

function ProjectWizard({ projects, onClose, onComplete }) {
  const [step, setStep] = useState(1);
  const [projectName, setProjectName] = useState('handoff-lab');
  const [useLocal, setUseLocal] = useState(false);
  const [useRemote, setUseRemote] = useState(true);
  const [machine, setMachine] = useState('devbox-01');
  const [activeLocation, setActiveLocation] = useState('remote');
  const [locationConfigs, setLocationConfigs] = useState({
    local: { sourceType: 'git', gitUrl: 'git@github.com:team/handoff-lab.git', path: '~/.handoff/handoff-lab', picked: false },
    remote: { sourceType: 'git', gitUrl: 'git@github.com:team/handoff-lab.git', path: '~/.handoff/handoff-lab', picked: false },
  });

  const defaultClonePath = `~/.handoff/${projectName.trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-') || 'project'}`;
  const selectedLocations = [
    ...(useLocal ? [{ id: 'local', label: '本机 · xushixin-mac' }] : []),
    ...(useRemote ? [{ id: 'remote', label: machine }] : []),
  ];
  const activeConfig = locationConfigs[activeLocation];
  const activeLabel = activeLocation === 'local' ? '本机 · xushixin-mac' : machine;
  const hasLocation = useLocal || useRemote;

  const updateLocation = (location, patch) => setLocationConfigs((current) => ({ ...current, [location]: { ...current[location], ...patch } }));

  const toggleLocal = () => {
    const next = !useLocal;
    setUseLocal(next);
    if (next) setActiveLocation('local');
    else if (useRemote) setActiveLocation('remote');
  };

  const toggleRemote = () => {
    const next = !useRemote;
    setUseRemote(next);
    if (next) setActiveLocation('remote');
    else if (useLocal) setActiveLocation('local');
  };

  const chooseSource = (nextSource) => {
    updateLocation(activeLocation, { sourceType: nextSource, picked: false, path: nextSource === 'git' ? defaultClonePath : '~/workspace/handoff-lab' });
  };

  const finish = () => {
    onComplete({
      id: projectName.trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-') || `project-${projects.length + 1}`,
      name: projectName.trim() || `project-${projects.length + 1}`,
      color: '#7b72e9',
      directories: selectedLocations.length,
      running: 0,
      attention: 0,
      host: selectedLocations.map((location) => location.label).join(' + '),
      locations: selectedLocations.map((location) => ({ ...location, ...locationConfigs[location.id] })),
    });
    onClose();
  };

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="modal project-wizard" role="dialog" aria-modal="true" aria-labelledby="add-title" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-heading">
          <div><h2 id="add-title">添加项目</h2><p>代码可位于本机和一台开发机，至少选择一个位置。</p></div>
          <IconButton label="关闭" onClick={onClose}><X size={16} /></IconButton>
        </div>
        <div className="wizard-steps" aria-label="添加项目进度">
          <span className={step === 1 ? 'active' : 'done'}><b>{step === 1 ? '1' : <Check size={11} />}</b>项目与宿主</span>
          <i />
          <span className={step === 2 ? 'active' : ''}><b>2</b>代码来源</span>
        </div>

        {step === 1 ? (
          <div className="wizard-body">
            <label>项目名称<input autoFocus value={projectName} onChange={(event) => { const nextName = event.target.value; const nextPath = `~/.handoff/${nextName.trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-') || 'project'}`; setProjectName(nextName); setLocationConfigs((current) => ({ local: current.local.sourceType === 'git' ? { ...current.local, path: nextPath } : current.local, remote: current.remote.sourceType === 'git' ? { ...current.remote, path: nextPath } : current.remote })); }} /></label>
            <div className="field-group"><span>代码位置 · 至少选择一个</span><div className="host-choice-grid">
              <button type="button" aria-pressed={useLocal} className={useLocal ? 'active' : ''} onClick={toggleLocal}><House size={17} /><span><strong>本机</strong><small>xushixin-mac</small></span><i className={`choice-check ${useLocal ? 'checked' : ''}`}>{useLocal && <Check size={11} />}</i></button>
              <button type="button" aria-pressed={useRemote} className={useRemote ? 'active' : ''} onClick={toggleRemote}><Server size={17} /><span><strong>开发机</strong><small>最多选择一台远程机器</small></span><i className={`choice-check ${useRemote ? 'checked' : ''}`}>{useRemote && <Check size={11} />}</i></button>
            </div></div>
            {useRemote && <label>选择开发机<select value={machine} onChange={(event) => setMachine(event.target.value)}><option>devbox-01</option><option>ci-runner-02</option></select></label>}
            <div className={`host-selection-status ${hasLocation ? 'valid' : 'invalid'}`}>{hasLocation ? <CheckCircle2 size={14} /> : <AlertCircle size={14} />}<span>{hasLocation ? `已选择 ${selectedLocations.length} 个代码位置${useLocal && useRemote ? '：本机 + 一台开发机' : ''}` : '请至少选择本机或一台开发机'}</span></div>
          </div>
        ) : (
          <div className="wizard-body">
            <div className="location-tabs">{selectedLocations.map((location, index) => <button type="button" className={activeLocation === location.id ? 'active' : ''} key={location.id} onClick={() => setActiveLocation(location.id)}>{location.id === 'local' ? <House size={13} /> : <Monitor size={13} />}<span>{location.label}</span><small>{index + 1}/{selectedLocations.length}</small></button>)}</div>
            <div className="segmented-control"><button type="button" className={activeConfig.sourceType === 'git' ? 'active' : ''} onClick={() => chooseSource('git')}><GitBranch size={13} />Git 仓库</button><button type="button" className={activeConfig.sourceType === 'directory' ? 'active' : ''} onClick={() => chooseSource('directory')}><FolderOpen size={13} />已有目录</button></div>
            {activeConfig.sourceType === 'git' ? <>
              <label>{activeLabel} · Git 地址<input autoFocus value={activeConfig.gitUrl} onChange={(event) => updateLocation(activeLocation, { gitUrl: event.target.value })} placeholder="git@github.com:team/project.git" /></label>
              <label>{activeLabel} · Clone 到（可选）<input value={activeConfig.path} onChange={(event) => updateLocation(activeLocation, { path: event.target.value })} placeholder={defaultClonePath} /><small>默认保存到 {defaultClonePath}</small></label>
            </> : activeLocation === 'local' ? <label>本机 · 项目目录<div className="path-picker"><input autoFocus value={activeConfig.path} onChange={(event) => updateLocation('local', { path: event.target.value })} /><button type="button" onClick={() => updateLocation('local', { picked: true, path: '/Users/xushixin/workspace/handoff-lab' })}><FolderOpen size={14} />在访达中选择</button></div>{activeConfig.picked && <small className="picked-hint"><Check size={11} />已从访达选择目录</small>}</label> : <label>{machine} · 项目目录<input autoFocus value={activeConfig.path} onChange={(event) => updateLocation('remote', { path: event.target.value })} placeholder="~/workspace/project" /><small>远程开发机仅支持直接粘贴目录 path。</small></label>}
            <div className="path-preview">{activeLocation === 'local' ? <House size={15} /> : <Monitor size={15} />}<div><strong>{activeLabel}</strong><span>{activeConfig.sourceType === 'git' ? `${activeConfig.gitUrl} → ${activeConfig.path || defaultClonePath}` : activeConfig.path}</span></div><BadgeCheck size={16} /></div>
          </div>
        )}

        <div className="modal-actions">
          <button type="button" onClick={step === 1 ? onClose : () => setStep(1)}>{step === 1 ? '取消' : '上一步'}</button>
          <button type="button" className="primary" disabled={step === 1 && !hasLocation} onClick={step === 1 ? () => setStep(2) : finish}>{step === 1 ? '继续配置目录' : '添加项目'}</button>
        </div>
      </section>
    </div>
  );
}

function LeftSidebar({ projects, currentView, onNavigate, selectedDirectory, onSelectDirectory, activeTask, onSelectTask, onAddProject }) {
  const [projectOpen, setProjectOpen] = useState(true);
  const [devboxOpen, setDevboxOpen] = useState(true);
  const [query, setQuery] = useState('');
  const [addOpen, setAddOpen] = useState(false);
  const visibleProjects = useMemo(() => projects.filter((project) => project.name.includes(query.trim().toLowerCase())), [projects, query]);

  return (
    <aside className="left-sidebar">
      <div className="window-nav">
        <span className="app-mark"><SquareTerminal size={17} /></span>
        <button className={`nav-item ${currentView === 'board' ? 'active' : ''}`} type="button" onClick={() => onNavigate('board')}><LayoutDashboard size={15} />任务看板</button>
        <button className={`nav-item ${currentView === 'machines' ? 'active' : ''}`} type="button" onClick={() => onNavigate('machines')}><Server size={15} />开发机</button>
      </div>

      <label className="sidebar-search">
        <Search size={15} />
        <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索项目、机器或任务" />
        <kbd>⌘K</kbd>
      </label>

      <div className="sidebar-section-title">项目 <span>{visibleProjects.length}</span></div>
      <div className="sidebar-tree">
        {visibleProjects.map((project) => (
          <div key={project.id}>
            <ProjectRow project={project} expanded={project.id === 'super-debug' && projectOpen} onToggle={() => project.id === 'super-debug' && setProjectOpen((value) => !value)} />
            {project.id === 'super-debug' && projectOpen && (
              <div className="project-children">
                <MachineRow name="devbox-01" connected expanded={devboxOpen} directories={2} running={2} onToggle={() => setDevboxOpen((value) => !value)} />
                {devboxOpen && (
                  <div className="machine-children">
                    <DirectoryRow label="主目录" detail="~/workspace/super-debug" icon={House} selected={currentView === 'workbench' && selectedDirectory === 'main'} onClick={() => onSelectDirectory('main')} />
                    <DirectoryRow label="integration/b2-b3" icon={GitBranch} selected={currentView === 'workbench' && selectedDirectory === 'integration/b2-b3'} onClick={() => onSelectDirectory('integration/b2-b3')} />
                    {selectedDirectory === 'integration/b2-b3' && (
                      <div className="task-list">
                        <TaskRow label="修复 DropPeer 并发测试" executor="OpenCode" age="23m" tone="online" active={currentView === 'workbench' && activeTask === 'running'} onClick={() => onSelectTask('running', 'integration/b2-b3')} />
                        <TaskRow label="等待批准：更新快照" executor="Codex" age="2m" tone="attention" active={currentView === 'workbench' && activeTask === 'approval'} onClick={() => onSelectTask('approval', 'integration/b2-b3')} />
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        ))}
      </div>

      <div className="sidebar-footer">
        <button type="button" className="add-project" onClick={() => setAddOpen(true)}><Plus size={15} />添加项目</button>
        <IconButton label="设置" className={currentView === 'settings' ? 'selected' : ''} onClick={() => onNavigate('settings')}><Settings size={16} /></IconButton>
      </div>

      {addOpen && <ProjectWizard projects={projects} onClose={() => setAddOpen(false)} onComplete={onAddProject} />}
    </aside>
  );
}

function ContextBar({ selectedDirectory, onNewTerminal }) {
  const [noticesOpen, setNoticesOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const directoryLabel = selectedDirectory === 'main' ? '主目录' : selectedDirectory;
  return (
    <header className="context-bar">
      <div className="breadcrumbs"><span>super-debug</span><i>/</i><span>devbox-01</span><i>/</i><strong>{directoryLabel}</strong></div>
      <span className="connection"><StatusDot />已连接</span>
      <span className="branch"><GitBranch size={14} />feat/droppeer-race-fix</span>
      <div className="context-actions">
        <IconButton label="新建终端" onClick={onNewTerminal}><SquareTerminal size={16} /></IconButton>
        <IconButton label="左右分屏"><Columns2 size={16} /></IconButton>
        <IconButton label="通知" onClick={() => setNoticesOpen((value) => !value)}><Bell size={16} /></IconButton>
        <IconButton label="账户" onClick={() => setProfileOpen((value) => !value)}><UserRound size={17} /></IconButton>
      </div>
      {noticesOpen && <div className="top-popover notice-popover"><strong>需要处理</strong><span>1 个 handoff 正在等待批准</span></div>}
      {profileOpen && <div className="top-popover profile-popover"><strong>xushixin</strong><span>本地控制台</span></div>}
    </header>
  );
}

function Tab({ active, icon: Icon, label, modified, onClick, close = true }) {
  return (
    <button className={`tab ${active ? 'active' : ''}`} type="button" onClick={onClick}>
      <Icon size={14} />
      <span>{label}</span>
      {modified && <b>M</b>}
      {close && <X size={13} className="tab-close" />}
    </button>
  );
}

function ToolCall({ icon: Icon, label, detail, done = true }) {
  return (
    <div className="tool-call">
      <span className="tool-icon">{done ? <Check size={12} /> : <Icon size={12} />}</span>
      <strong>{label}</strong>
      <code>{detail}</code>
    </div>
  );
}

function highlightGo(line) {
  const tokenPattern = /(\s*\/\/.*$|".*?"|\b(?:func|defer|for|range|var|go|if|nil|close|return|type|struct|make|chan|error)\b|\b\d+\b)/g;
  return line.split(tokenPattern).filter(Boolean).map((part, index) => {
    const value = part.trim();
    let className = '';
    if (value.startsWith('//')) className = 'token-comment';
    else if (value.startsWith('"')) className = 'token-string';
    else if (/^\d+$/.test(value)) className = 'token-number';
    else if (/^(func|defer|for|range|var|go|if|nil|close|return|type|struct|make|chan|error)$/.test(value)) className = 'token-keyword';
    return <span className={className} key={`${index}-${part}`}>{part}</span>;
  });
}

function TaskTui({ activeTask, setActiveTask }) {
  const [approvalState, setApprovalState] = useState(null);
  const [draft, setDraft] = useState('');
  const [sentPrompt, setSentPrompt] = useState('');
  const isApproval = activeTask === 'approval';
  const submit = (event) => {
    event.preventDefault();
    if (!draft.trim()) return;
    setSentPrompt(draft.trim());
    setDraft('');
  };

  if (!activeTask) {
    return (
      <div className="empty-terminal">
        <SquareTerminal size={28} />
        <strong>终端已准备</strong>
        <span>从左侧选择 handoff 任务，或直接输入命令。</span>
        <button type="button" onClick={() => setActiveTask('running')}>打开最近任务</button>
      </div>
    );
  }

  return (
    <div className="tui-surface">
      <div className="tui-meta">
        <strong>{isApproval ? 'Codex CLI v0.18.1' : 'OpenCode CLI v1.18.15'}</strong>
        <span>Model: gpt-5-codex</span>
        <span>Context: 144,390 tokens (14%)</span>
      </div>
      <div className="tui-scroll">
        <div className="speaker user">user</div>
        <p>{isApproval ? '更新并发测试的快照，并确认变更不会覆盖其他工作树。' : '修复 DropPeer 并发测试偶现失败的问题。'}</p>
        <div className="speaker assistant">assistant</div>
        <p>{isApproval ? '测试已经通过。更新快照会执行下面的命令，需要你的批准。' : '我会检查并发相关的测试，修复竞态条件，然后运行测试验证。'}</p>

        <ToolCall icon={SearchCode} label="读取文件" detail="internal/cluster/transport_test.go" />
        <ToolCall icon={SearchCode} label="读取文件" detail="internal/cluster/transport.go" />
        {!isApproval && <><div className="speaker assistant">assistant</div><p>发现测试中并发启动的 goroutine 存在数据竞争，我将调整同步逻辑。</p></>}
        <ToolCall icon={FilePenLine} label="编辑文件" detail="internal/cluster/transport_test.go (89–117)" />
        {!isApproval && <ToolCall icon={FilePenLine} label="编辑文件" detail="internal/cluster/transport.go (78–92)" />}
        {!isApproval && <><div className="speaker assistant">assistant</div><p>运行测试验证修复结果。</p></>}
        {!isApproval && <ToolCall icon={Play} label="运行命令" detail="go test ./internal/cluster -run DropPeer -count=50 -race" done={false} />}

        {isApproval && approvalState === null && (
          <div className="approval-box">
            <span className="approval-title"><TriangleAlert size={14} />需要你的批准</span>
            <code>go test ./... -run TestDropPeerDrainQueue -update -count=1</code>
            <p>此操作将更新相关测试快照文件。</p>
            <div><button type="button" onClick={() => setApprovalState('allowed')}>允许一次</button><button type="button" onClick={() => setApprovalState('denied')}>拒绝</button></div>
          </div>
        )}
        {isApproval && approvalState === 'allowed' && <div className="decision allowed"><Check size={14} />已批准，命令开始执行。</div>}
        {isApproval && approvalState === 'denied' && <div className="decision denied"><X size={14} />已拒绝，任务保持等待。</div>}

        {!isApproval && (
          <>
            <div className="todo-block">
              <div>Todo</div>
              <span><Check size={13} />检查并发测试超时与竞态</span>
              <span><Check size={13} />修复 DropPeer 并发测试</span>
              <span><Check size={13} />修复 transport.go 同步逻辑</span>
              <span className="current"><Play size={12} />运行并发测试验证</span>
            </div>
            <div className="current-action">
              <small>当前操作</small>
              <code>运行 go test ./internal/cluster -run DropPeer -count=50 -race</code>
              <div className="test-result"><span>ok</span><code>github.com/super-debug/transport</code><time>12.851s</time></div>
              <div className="test-result"><span>ok</span><code>github.com/super-debug/cluster</code><time>18.237s</time></div>
            </div>
            <div className="tui-finish">
              <div className="speaker assistant">assistant</div>
              <p>所有测试通过。建议更新快照并提交这次并发修复。</p>
            </div>
          </>
        )}
        {sentPrompt && <div className="sent-message"><span>user</span><p>{sentPrompt}</p></div>}
      </div>
      <form className="tui-composer" onSubmit={submit}>
        <input value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="输入你的指令或问题…" />
        <button type="submit" aria-label="发送"><Send size={15} /></button>
        <div className="composer-hints"><span><Command size={12} />K 清空</span><span>/ 触发命令</span><span><AtSign size={12} />引用</span></div>
      </form>
    </div>
  );
}

function CodeEditor({ selectedFile }) {
  const rows = codeByFile[selectedFile] || codeByFile['transport_test.go'];
  return (
    <div className="editor-surface">
      <div className="editor-breadcrumb">internal <ChevronRight size={12} /> cluster <ChevronRight size={12} /><FileCode2 size={13} /><span>{selectedFile}</span></div>
      <div className="code-scroll">
        {rows.map(([number, line]) => {
          const lineNumber = Number(number);
          const highlight = selectedFile === 'transport_test.go' && lineNumber >= 78 && lineNumber <= 94;
          return (
            <div className={`code-line ${highlight ? 'highlight' : ''}`} key={`${selectedFile}-${number}`}>
              <span className="line-number">{number}</span>
              <code>{line ? highlightGo(line) : ' '}</code>
            </div>
          );
        })}
      </div>
      <div className="editor-status"><span>Go</span><span>行 78–94, 已修改</span><span>UTF-8</span><span>LF</span></div>
    </div>
  );
}

function BrowserPreview() {
  return (
    <div className="browser-surface">
      <div className="browser-address"><Globe2 size={14} /><span>http://localhost:5173</span><RefreshCw size={13} /></div>
      <div className="browser-page">
        <span className="preview-kicker">super-debug / integration/b2-b3</span>
        <h2>Transport health</h2>
        <p>Local preview from devbox-01</p>
        <div className="preview-list">
          <span><StatusDot />Peer manager <b>Healthy</b></span>
          <span><StatusDot />Drain queue <b>Healthy</b></span>
          <span><StatusDot tone="attention" />Snapshot <b>Needs approval</b></span>
        </div>
      </div>
    </div>
  );
}

function ShellPanel({ open, setOpen }) {
  return (
    <section className={`shell-panel ${open ? '' : 'collapsed'}`}>
      <header>
        <div className="shell-tabs"><button className="active" type="button">终端</button><button type="button">问题</button><button type="button">输出</button><button type="button">调试控制台</button></div>
        <div className="shell-actions"><span>bash</span><IconButton label={open ? '收起终端' : '展开终端'} onClick={() => setOpen((value) => !value)}>{open ? <PanelBottomClose size={14} /> : <PanelBottomOpen size={14} />}</IconButton><IconButton label="更多"><MoreHorizontal size={15} /></IconButton></div>
      </header>
      {open && (
        <div className="shell-output">
          <p><strong>devbox-01:~/workspace/super-debug/integration/b2-b3$</strong> go test ./internal/cluster -run DropPeer -count=50 -race</p>
          <p>ok&nbsp;&nbsp;&nbsp; github.com/super-debug/transport&nbsp;&nbsp;&nbsp;&nbsp; 12.851s</p>
          <p>ok&nbsp;&nbsp;&nbsp; github.com/super-debug/cluster&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp; 18.237s</p>
          <p>PASS</p>
          <p>ok&nbsp;&nbsp;&nbsp; github.com/super-debug/transport&nbsp;&nbsp;&nbsp;&nbsp;&nbsp; 0.698s</p>
          <p><strong>devbox-01:~/workspace/super-debug/integration/b2-b3$</strong> <span className="cursor" /></p>
        </div>
      )}
    </section>
  );
}

function Workspace({ activeTask, setActiveTask, selectedFile, setSelectedFile }) {
  const [rightTab, setRightTab] = useState('editor');
  const [shellOpen, setShellOpen] = useState(true);
  return (
    <main className="workspace">
      <section className="tab-group tui-group">
        <div className="tab-strip">
          <Tab active icon={Bot} label={activeTask === 'approval' ? 'Codex · 等待批准' : activeTask ? 'OpenCode · 修复 DropPeer 并发测试' : 'Terminal · b2-b3'} />
          <span className="handoff-live">via handoff · live <StatusDot /></span>
          <IconButton label="新建标签"><Plus size={15} /></IconButton>
        </div>
        <TaskTui activeTask={activeTask} setActiveTask={setActiveTask} />
      </section>
      <section className="tab-group editor-group">
        <div className="tab-strip editor-tabs">
          <Tab active={rightTab === 'editor'} icon={FileCode2} label={selectedFile} modified onClick={() => setRightTab('editor')} />
          <Tab active={rightTab === 'browser'} icon={Globe2} label="localhost:5173" onClick={() => setRightTab('browser')} />
          <IconButton label="新建标签"><Plus size={15} /></IconButton>
        </div>
        <div className="right-group-body">
          {rightTab === 'editor' ? <CodeEditor selectedFile={selectedFile} /> : <BrowserPreview />}
          <ShellPanel open={shellOpen} setOpen={setShellOpen} />
        </div>
      </section>
    </main>
  );
}

function PageHeader({ eyebrow, title, description, actions }) {
  return (
    <header className="page-header">
      <div><span>{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}

function TaskBoardCard({ task, tone, onOpenTask }) {
  const canOpen = Boolean(task.taskKey);
  const dotTone = tone === 'attention' ? 'attention' : tone === 'neutral' ? 'neutral' : 'online';
  return (
    <button className={`board-card ${task.attention ? 'needs-attention' : ''} ${canOpen ? 'actionable' : ''}`} type="button" onClick={() => canOpen && onOpenTask(task.taskKey, task.directory)}>
      <div className="board-card-top"><code>{task.id}</code><time>{task.age}</time></div>
      <strong>{task.title}</strong>
      <div className="board-context"><span><Boxes size={12} />{task.project}</span><span><GitBranch size={12} />{task.directory}</span><span><Monitor size={12} />{task.machine}</span></div>
      {task.progress != null && <div className="task-progress"><i><b style={{ width: `${task.progress}%` }} /></i><span>{task.progress}%</span></div>}
      <div className="board-card-footer"><span className={`task-state ${tone}`}><StatusDot tone={dotTone} />{task.state}</span><span><Bot size={12} />{task.executor}</span></div>
    </button>
  );
}

function TaskBoardPage({ onOpenTask }) {
  const [query, setQuery] = useState('');
  const [attentionOnly, setAttentionOnly] = useState(false);
  const [projectFilterOpen, setProjectFilterOpen] = useState(false);
  const allProjects = useMemo(() => [...new Set(boardColumns.flatMap((column) => column.tasks.map((task) => task.project)))], []);
  const [selectedProjects, setSelectedProjects] = useState(allProjects);
  const visibleColumns = boardColumns.map((column) => ({
    ...column,
    tasks: column.tasks.filter((task) => {
      const matchesQuery = `${task.title} ${task.project} ${task.machine} ${task.executor}`.toLowerCase().includes(query.toLowerCase());
      return matchesQuery && selectedProjects.includes(task.project) && (!attentionOnly || task.attention);
    }),
  }));
  const visibleTasks = visibleColumns.reduce((sum, column) => sum + column.tasks.length, 0);
  const projectFilterLabel = selectedProjects.length === allProjects.length ? '全部项目' : `${selectedProjects.length} 个项目`;
  const toggleProject = (project) => setSelectedProjects((current) => current.includes(project) ? current.filter((item) => item !== project) : [...current, project]);

  return (
    <section className="global-page board-page">
      <PageHeader
        eyebrow="跨项目任务"
        title="任务看板"
        description="所有开发机上的 handoff 任务，共用同一份实时状态。"
        actions={<button className={`quiet-button ${attentionOnly ? 'active' : ''}`} type="button" onClick={() => setAttentionOnly((value) => !value)}><TriangleAlert size={14} />只看待处理</button>}
      />
      <div className="board-toolbar">
        <label><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务、项目或执行者" /></label>
        <div className="project-filter-wrap">
          <button className={`filter-control ${projectFilterOpen ? 'active' : ''}`} type="button" aria-haspopup="menu" aria-expanded={projectFilterOpen} onClick={() => setProjectFilterOpen((value) => !value)}><ListFilter size={14} />{projectFilterLabel}<ChevronDown size={13} /></button>
          {projectFilterOpen && <div className="filter-popover" role="menu">
            <header><strong>筛选项目</strong><span>可多选</span></header>
            <div className="filter-options">{allProjects.map((project) => {
              const checked = selectedProjects.includes(project);
              return <button type="button" className="filter-option" role="menuitemcheckbox" aria-checked={checked} key={project} onClick={() => toggleProject(project)}><span className={`filter-check ${checked ? 'checked' : ''}`}>{checked && <Check size={11} />}</span><Boxes size={13} /><span>{project}</span></button>;
            })}</div>
            <footer><button type="button" onClick={() => setSelectedProjects([])}>清空</button><button type="button" onClick={() => setSelectedProjects(allProjects)}>全选</button></footer>
          </div>}
        </div>
        <span>全部开发机</span><b>{visibleTasks} 个任务</b>
      </div>
      <div className="board-grid">
        {visibleColumns.map((column) => (
          <section className={`board-column ${column.tone}`} key={column.id}>
            <header><span><StatusDot tone={column.tone === 'attention' ? 'attention' : column.tone === 'done' ? 'online' : column.tone} />{column.title}</span><b>{column.tasks.length}</b></header>
            <div className="board-column-body">
              {column.tasks.length ? column.tasks.map((task) => <TaskBoardCard key={task.id} task={task} tone={column.tone} onOpenTask={onOpenTask} />) : <div className="board-empty"><CheckCircle2 size={20} /><span>没有匹配的任务</span></div>}
            </div>
          </section>
        ))}
      </div>
    </section>
  );
}

const initialMachines = [
  { id: 'devbox-01', name: 'devbox-01', host: '100.84.251.46', platform: 'Ubuntu 24.04', transport: 'SSH', connected: true, latency: '34 ms', directories: 6, tasks: 2, version: 'agentd 1.6.3', lastSeen: '刚刚' },
  { id: 'ci-runner-02', name: 'ci-runner-02', host: 'runner.internal', platform: 'Docker · Linux', transport: 'SSH', connected: true, latency: '68 ms', directories: 2, tasks: 0, version: 'agentd 1.6.3', lastSeen: '刚刚' },
  { id: 'staging-03', name: 'staging-03', host: '10.0.8.23', platform: 'Ubuntu 22.04', transport: 'SSH', connected: false, latency: '—', directories: 1, tasks: 0, version: 'agentd 1.5.9', lastSeen: '18 分钟前' },
];

const machineEnvFileNames = {
  'devbox-01': ['default.env', 'openai-team.env', 'reviewer.env'],
  'ci-runner-02': ['ci.env', 'release.env'],
  'staging-03': ['staging.env'],
};

const initialMachineRuntime = {
  'devbox-01': {
    executors: {
      OpenCode: { enabled: true, envFile: 'openai-team.env' },
      Codex: { enabled: true, envFile: 'default.env' },
      'Claude Code': { enabled: false, envFile: '' },
    },
    approver: { executor: 'Claude Code', envFile: 'reviewer.env' },
  },
  'ci-runner-02': {
    executors: {
      OpenCode: { enabled: false, envFile: '' },
      Codex: { enabled: true, envFile: 'ci.env' },
      'Claude Code': { enabled: false, envFile: '' },
    },
    approver: { executor: 'Codex', envFile: 'release.env' },
  },
  'staging-03': {
    executors: {
      OpenCode: { enabled: true, envFile: 'staging.env' },
      Codex: { enabled: false, envFile: '' },
      'Claude Code': { enabled: false, envFile: '' },
    },
    approver: { executor: '', envFile: '' },
  },
};

function PairMachineModal({ onClose, onComplete }) {
  const [name, setName] = useState('lab-macbook');
  const [host, setHost] = useState('192.168.1.88');
  const [copied, setCopied] = useState(false);
  const command = `handoff agent pair --name ${name} --token HD7K-Q2PV`;

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="modal pair-modal" role="dialog" aria-modal="true" aria-labelledby="pair-title" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-heading"><div><h2 id="pair-title">配对开发机</h2><p>在目标开发机上运行一次性配对命令。</p></div><IconButton label="关闭" onClick={onClose}><X size={16} /></IconButton></div>
        <div className="pair-fields"><label>显示名称<input value={name} onChange={(event) => setName(event.target.value)} /></label><label>主机地址<input value={host} onChange={(event) => setHost(event.target.value)} /></label></div>
        <div className="pair-command"><span>在目标机器执行</span><code>{command}</code><button type="button" onClick={() => setCopied(true)}><Copy size={14} />{copied ? '已复制' : '复制命令'}</button></div>
        <div className="security-note"><ShieldCheck size={17} /><span>令牌仅用于本次配对，5 分钟后失效。桌面端不会远程安装或升级 agent。</span></div>
        <div className="modal-actions"><button type="button" onClick={onClose}>取消</button><button type="button" className="primary" onClick={() => { onComplete({ id: name, name, host, platform: 'macOS 15.6', transport: 'SSH', connected: true, latency: '22 ms', directories: 0, tasks: 0, version: 'agentd 1.6.3', lastSeen: '刚刚' }); onClose(); }}>完成配对</button></div>
      </section>
    </div>
  );
}

function MachineCard({ machine, selected, onSelect }) {
  return (
    <button type="button" className={`machine-card ${selected ? 'selected' : ''} ${machine.connected ? '' : 'offline'}`} onClick={onSelect}>
      <div className="machine-card-icon">{machine.connected ? <Monitor size={19} /> : <WifiOff size={19} />}</div>
      <div className="machine-card-main"><strong>{machine.name}</strong><span>{machine.platform}</span><small>{machine.host} · {machine.transport}</small></div>
      <div className="machine-card-meta"><span><StatusDot tone={machine.connected ? 'online' : 'offline'} />{machine.connected ? '已连接' : '已断开'}</span><small>{machine.connected ? machine.latency : machine.lastSeen}</small></div>
    </button>
  );
}

function MachinesPage({ onOpenDirectory }) {
  const [machines, setMachines] = useState(initialMachines);
  const [selectedId, setSelectedId] = useState('devbox-01');
  const [pairOpen, setPairOpen] = useState(false);
  const [runtimeByMachine, setRuntimeByMachine] = useState(initialMachineRuntime);
  const selected = machines.find((machine) => machine.id === selectedId) || machines[0];
  const selectedRuntime = runtimeByMachine[selected.id] || { executors: { OpenCode: { enabled: true, envFile: '' }, Codex: { enabled: true, envFile: '' }, 'Claude Code': { enabled: false, envFile: '' } }, approver: { executor: '', envFile: '' } };
  const envFiles = machineEnvFileNames[selected.id] || [];
  const retry = () => setMachines((items) => items.map((machine) => machine.id === selected.id ? { ...machine, connected: true, latency: '46 ms', lastSeen: '刚刚' } : machine));
  const updateExecutor = (executor, patch) => setRuntimeByMachine((current) => ({ ...current, [selected.id]: { ...selectedRuntime, executors: { ...selectedRuntime.executors, [executor]: { ...selectedRuntime.executors[executor], ...patch } } } }));
  const updateApprover = (patch) => setRuntimeByMachine((current) => ({ ...current, [selected.id]: { ...selectedRuntime, approver: { ...selectedRuntime.approver, ...patch } } }));

  return (
    <section className="global-page machines-page">
      <PageHeader eyebrow="运行环境" title="开发机与 agent" description="管理已经配对的开发机、连接能力和可用执行者。" actions={<button className="primary-button" type="button" onClick={() => setPairOpen(true)}><Cable size={14} />配对开发机</button>} />
      <div className="machine-summary"><span><Monitor size={16} /><b>{machines.length}</b>台开发机</span><span><Wifi size={16} /><b>{machines.filter((machine) => machine.connected).length}</b>台在线</span><span><Activity size={16} /><b>{machines.reduce((sum, machine) => sum + machine.tasks, 0)}</b>个任务运行中</span></div>
      <div className="machines-layout">
        <div className="machine-list-panel">
          <div className="panel-title"><strong>已配对</strong><span>{machines.length}</span></div>
          <div className="machine-list">{machines.map((machine) => <MachineCard key={machine.id} machine={machine} selected={machine.id === selected.id} onSelect={() => setSelectedId(machine.id)} />)}</div>
        </div>
        <aside className="machine-detail-panel">
          <header><div><span><StatusDot tone={selected.connected ? 'online' : 'offline'} />{selected.connected ? '已连接' : '已断开'}</span><h2>{selected.name}</h2><p>{selected.host} · {selected.platform}</p></div><IconButton label="更多"><MoreHorizontal size={16} /></IconButton></header>
          {!selected.connected && <div className="offline-callout"><WifiOff size={18} /><div><strong>该开发机当前不可用</strong><span>文件、终端和任务现场均不可读取。</span></div><button type="button" onClick={retry}>重试连接</button></div>}
          <section className="detail-section"><h3>连接</h3><dl><div><dt>传输方式</dt><dd>{selected.transport}</dd></div><div><dt>延迟</dt><dd>{selected.latency}</dd></div><div><dt>Agent</dt><dd>{selected.version}</dd></div><div><dt>最后心跳</dt><dd>{selected.lastSeen}</dd></div></dl></section>
          <section className="detail-section"><h3>目录与任务</h3><div className="detail-metrics"><span><b>{selected.directories}</b>项目目录</span><span><b>{selected.tasks}</b>运行任务</span></div>{selected.id === 'devbox-01' && <button className="inline-link" type="button" onClick={() => onOpenDirectory('integration/b2-b3')}><FolderOpen size={14} />打开 integration/b2-b3<ChevronRight size={14} /></button>}</section>
          <section className="detail-section"><h3>可用执行者</h3><p className="section-hint">每个执行者可选一份本机 Env 文件，启动任务时加载。</p>{Object.entries(selectedRuntime.executors).map(([executor, config]) => <div className="executor-row" key={executor}><span className="executor-icon"><Bot size={14} /></span><div><strong>{executor}</strong><small>{config.enabled ? '可在此开发机启动' : '已禁用'}</small></div><label className="env-file-select"><FileText size={12} /><select aria-label={`${executor} Env 文件`} value={config.envFile} disabled={!selected.connected} onChange={(event) => updateExecutor(executor, { envFile: event.target.value })}><option value="">不使用 Env</option>{envFiles.map((file) => <option key={file}>{file}</option>)}</select></label><button className={`mini-toggle ${config.enabled ? 'on' : ''}`} type="button" disabled={!selected.connected} aria-label={`${config.enabled ? '禁用' : '启用'} ${executor}`} onClick={() => updateExecutor(executor, { enabled: !config.enabled })}><i /></button></div>)}</section>
          <section className="detail-section approver-section"><h3>自动审批者</h3><p className="section-hint">handoff 轻量调用一个执行者处理分级审批；不确定时才上交用户。</p><div className="approver-fields"><label><span>审批执行者</span><select aria-label="审批执行者" value={selectedRuntime.approver.executor} disabled={!selected.connected} onChange={(event) => updateApprover({ executor: event.target.value, envFile: event.target.value ? selectedRuntime.approver.envFile : '' })}><option value="">不启用自动审批</option>{Object.keys(selectedRuntime.executors).map((executor) => <option key={executor}>{executor}</option>)}</select></label><label><span>Env 文件 · 可选</span><select aria-label="审批者 Env 文件" value={selectedRuntime.approver.envFile} disabled={!selected.connected || !selectedRuntime.approver.executor} onChange={(event) => updateApprover({ envFile: event.target.value })}><option value="">不使用 Env</option>{envFiles.map((file) => <option key={file}>{file}</option>)}</select></label></div><div className="approval-chain"><span>任务执行者</span><ChevronRight size={12} /><span>自动审批者</span><ChevronRight size={12} /><strong>用户（仅升级时）</strong></div></section>
          <div className="machine-actions"><button type="button" disabled={!selected.connected}><RefreshCw size={14} />重启 agent</button><button type="button" disabled={!selected.connected}><SquareTerminal size={14} />打开终端</button></div>
        </aside>
      </div>
      {pairOpen && <PairMachineModal onClose={() => setPairOpen(false)} onComplete={(machine) => { setMachines((items) => [...items, machine]); setSelectedId(machine.id); }} />}
    </section>
  );
}

function Toggle({ value, onChange, label }) {
  return <button className={`settings-toggle ${value ? 'on' : ''}`} type="button" role="switch" aria-checked={value} aria-label={label} onClick={() => onChange(!value)}><i /></button>;
}

function SettingRow({ title, description, children }) {
  return <div className="setting-row"><div><strong>{title}</strong><span>{description}</span></div><div className="setting-control">{children}</div></div>;
}

function SettingsPage() {
  const [section, setSection] = useState('general');
  const [saved, setSaved] = useState(false);
  const [theme, setTheme] = useState('跟随系统');
  const [startAtLogin, setStartAtLogin] = useState(true);
  const [restoreTabs, setRestoreTabs] = useState(true);
  const [autoUpdate, setAutoUpdate] = useState(true);
  const [selectedEnvHost, setSelectedEnvHost] = useState('local');
  const [selectedEnvFile, setSelectedEnvFile] = useState('default.env');
  const [envHosts, setEnvHosts] = useState([
    { id: 'local', name: '本机 · xushixin-mac', connected: true, path: '~/.handoff/env/', files: [
      { name: 'default.env', modified: '刚刚', variables: [{ id: 1, key: 'HANDOFF_LOG_LEVEL', value: 'info' }, { id: 2, key: 'EDITOR', value: 'code --wait' }] },
      { name: 'personal.env', modified: '昨天', variables: [{ id: 3, key: 'GITHUB_TOKEN', value: '••••••••••••••••' }] },
    ] },
    { id: 'devbox-01', name: 'devbox-01', connected: true, path: '~/.handoff/env/', files: [
      { name: 'openai-team.env', modified: '4 分钟前', variables: [{ id: 4, key: 'OPENAI_API_KEY', value: '••••••••••••••••' }, { id: 5, key: 'OPENAI_BASE_URL', value: 'https://api.openai.com/v1' }] },
      { name: 'reviewer.env', modified: '2 小时前', variables: [{ id: 6, key: 'APPROVER_MODEL', value: 'gpt-5-codex' }, { id: 7, key: 'APPROVER_REASONING', value: 'low' }] },
      { name: 'default.env', modified: '3 天前', variables: [{ id: 8, key: 'GOFLAGS', value: '-count=1' }] },
    ] },
    { id: 'ci-runner-02', name: 'ci-runner-02', connected: true, path: '~/.handoff/env/', files: [
      { name: 'ci.env', modified: '今天', variables: [{ id: 9, key: 'CI', value: 'true' }, { id: 10, key: 'GITHUB_TOKEN', value: '••••••••••••••••' }] },
      { name: 'release.env', modified: '5 天前', variables: [{ id: 11, key: 'RELEASE_CHANNEL', value: 'canary' }] },
    ] },
    { id: 'staging-03', name: 'staging-03', connected: false, path: '~/.handoff/env/', files: [] },
  ]);
  const sections = [
    { id: 'general', label: '基础设置', icon: SlidersHorizontal },
    { id: 'env', label: 'Env 文件', icon: FileText },
  ];

  const save = () => setSaved(true);
  const currentEnvHost = envHosts.find((host) => host.id === selectedEnvHost) || envHosts[0];
  const currentEnvFile = currentEnvHost.files.find((file) => file.name === selectedEnvFile) || currentEnvHost.files[0];
  const chooseEnvHost = (host) => { setSelectedEnvHost(host.id); setSelectedEnvFile(host.files[0]?.name || ''); };
  const updateCurrentEnvFile = (update) => setEnvHosts((hosts) => hosts.map((host) => host.id !== currentEnvHost.id ? host : { ...host, files: host.files.map((file) => file.name !== currentEnvFile.name ? file : update(file)) }));
  const addEnvFile = () => {
    const baseName = 'new-profile.env';
    let name = baseName;
    let suffix = 2;
    while (currentEnvHost.files.some((file) => file.name === name)) name = `new-profile-${suffix++}.env`;
    setEnvHosts((hosts) => hosts.map((host) => host.id === currentEnvHost.id ? { ...host, files: [...host.files, { name, modified: '刚刚', variables: [] }] } : host));
    setSelectedEnvFile(name);
  };
  const addEnvVariable = () => updateCurrentEnvFile((file) => ({ ...file, modified: '刚刚', variables: [...file.variables, { id: Date.now(), key: 'NEW_VARIABLE', value: '' }] }));
  const deleteEnvVariable = (id) => updateCurrentEnvFile((file) => ({ ...file, modified: '刚刚', variables: file.variables.filter((row) => row.id !== id) }));
  const editEnvVariable = (id, patch) => updateCurrentEnvFile((file) => ({ ...file, modified: '刚刚', variables: file.variables.map((row) => row.id === id ? { ...row, ...patch } : row) }));

  return (
    <section className="global-page settings-page">
      <PageHeader eyebrow="桌面端" title="设置" description="控制本地体验，以及 handoff 在不同开发环境中的默认行为。" actions={<button className="primary-button" type="button" onClick={save}><Save size={14} />{saved ? '已保存' : '保存更改'}</button>} />
      <div className="settings-layout">
        <nav className="settings-nav">{sections.map(({ id, label, icon: Icon }) => <button className={section === id ? 'active' : ''} type="button" key={id} onClick={() => { setSection(id); setSaved(false); }}><Icon size={15} />{label}<ChevronRight size={14} /></button>)}</nav>
        <div className="settings-content">
          {section === 'general' && <>
            <div className="settings-section-heading"><h2>基础设置</h2><p>控制桌面端启动、会话恢复与更新行为。</p></div>
            <section className="settings-card"><SettingRow title="登录时启动" description="系统登录后在后台启动控制台。"><Toggle value={startAtLogin} onChange={setStartAtLogin} label="登录时启动" /></SettingRow><SettingRow title="恢复工作台" description="重新打开上次的标签页、目录与分屏。"><Toggle value={restoreTabs} onChange={setRestoreTabs} label="恢复工作台" /></SettingRow><SettingRow title="主题" description="只影响桌面外壳，终端主题单独配置。"><select value={theme} onChange={(event) => setTheme(event.target.value)}><option>跟随系统</option><option>浅色</option><option>深色</option></select></SettingRow><SettingRow title="默认 Shell" description="新建本地终端时使用。"><input value="/bin/zsh" readOnly /></SettingRow><SettingRow title="自动检查更新" description="仅提示可用版本，不自动重启。"><Toggle value={autoUpdate} onChange={setAutoUpdate} label="自动检查更新" /></SettingRow></section>
          </>}

          {section === 'env' && <>
            <div className="settings-section-heading"><h2>Env 文件</h2><p>每台机器在自己的 handoff 目录中维护多份 .env 文件；每份文件包含一组环境变量。</p></div>
            <div className="env-host-tabs">{envHosts.map((host) => <button type="button" className={`${selectedEnvHost === host.id ? 'active' : ''} ${host.connected ? '' : 'offline'}`} key={host.id} onClick={() => chooseEnvHost(host)}><StatusDot tone={host.connected ? 'online' : 'offline'} />{host.name}</button>)}</div>
            {!currentEnvHost.connected ? <div className="env-offline"><WifiOff size={19} /><div><strong>{currentEnvHost.name} 已断开</strong><span>机器断开后，handoff 目录中的 Env 文件不可读取或编辑。</span></div></div> : <>
              <div className="env-location"><FolderOpen size={15} /><div><span>文件目录</span><code>{currentEnvHost.name} · {currentEnvHost.path}</code></div><b>{currentEnvHost.files.length} 份文件</b></div>
              <div className="env-files-layout">
                <aside className="env-file-list"><header><strong>Env 文件</strong><button type="button" onClick={addEnvFile}><Plus size={13} />新建</button></header>{currentEnvHost.files.map((file) => <button type="button" className={selectedEnvFile === file.name ? 'active' : ''} key={file.name} onClick={() => setSelectedEnvFile(file.name)}><FileText size={14} /><span><strong>{file.name}</strong><small>{file.variables.length} 个变量 · {file.modified}</small></span><ChevronRight size={13} /></button>)}</aside>
                <section className="env-file-editor">{currentEnvFile ? <><header><div><FileText size={16} /><span><strong>{currentEnvFile.name}</strong><small>{currentEnvHost.path}{currentEnvFile.name}</small></span></div><button type="button"><MoreHorizontal size={15} /></button></header><div className="env-file-table-head"><span>变量名</span><span>值</span><span /></div>{currentEnvFile.variables.map((row) => <div className="env-file-row" key={row.id}><input value={row.key} onChange={(event) => editEnvVariable(row.id, { key: event.target.value })} /><input value={row.value} onChange={(event) => editEnvVariable(row.id, { value: event.target.value })} /><IconButton label={`删除 ${row.key}`} onClick={() => deleteEnvVariable(row.id)}><Trash2 size={14} /></IconButton></div>)}<button className="add-env" type="button" onClick={addEnvVariable}><Plus size={14} />添加变量</button></> : <div className="env-file-empty"><FileText size={20} /><span>新建一份 Env 文件开始配置</span><button type="button" onClick={addEnvFile}><Plus size={13} />新建 Env 文件</button></div>}</section>
              </div>
              <div className="effective-config"><Variable size={18} /><div><strong>按文件加载</strong><span>执行者与自动审批者可以在对应开发机页面选择其中一份 Env 文件，也可以不选择。</span></div></div>
            </>}
          </>}
        </div>
      </div>
    </section>
  );
}

function FileTree({ selectedDirectory, selectedFile, setSelectedFile }) {
  const [expanded, setExpanded] = useState(new Set(['internal', 'cluster']));
  const [query, setQuery] = useState('');
  const toggleFolder = (name) => {
    setExpanded((current) => {
      const next = new Set(current);
      next.has(name) ? next.delete(name) : next.add(name);
      return next;
    });
  };
  const visibleRows = fileRows.filter((row) => {
    if (query && !row.name.toLowerCase().includes(query.toLowerCase())) return false;
    if (!query && row.depth === 1 && !expanded.has('internal')) return false;
    if (!query && row.depth === 2 && (!expanded.has('internal') || !expanded.has('cluster'))) return false;
    return true;
  });
  return (
    <aside className="file-sidebar">
      <div className="file-header"><strong>文件</strong><div><IconButton label="刷新"><RefreshCw size={15} /></IconButton><IconButton label="更多"><MoreHorizontal size={16} /></IconButton><IconButton label="收起侧边栏"><PanelRight size={16} /></IconButton></div></div>
      <label className="file-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索文件" /></label>
      <div className="file-root"><span>{selectedDirectory === 'main' ? 'super-debug' : selectedDirectory}</span><small>{selectedDirectory === 'main' ? '~/workspace/super-debug' : ''}</small></div>
      <div className="file-tree">
        {visibleRows.map((row) => {
          const isFolder = row.type === 'folder';
          const isOpen = expanded.has(row.name);
          const selected = !isFolder && row.name === selectedFile;
          return (
            <button
              key={`${row.depth}-${row.name}`}
              type="button"
              className={`file-row ${selected ? 'selected' : ''}`}
              style={{ paddingLeft: 12 + row.depth * 18 }}
              onClick={() => isFolder ? toggleFolder(row.name) : setSelectedFile(row.name)}
            >
              {isFolder ? (isOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />) : <span className="file-chevron" />}
              {isFolder ? (isOpen ? <FolderOpen size={14} /> : <Folder size={14} />) : row.name.endsWith('.md') ? <FileText size={14} /> : <FileCode2 size={14} />}
              <span>{row.name}</span>
              {row.modified && <b>M</b>}
            </button>
          );
        })}
      </div>
    </aside>
  );
}

function StatusBar({ selectedDirectory, currentView }) {
  const globalLabels = { board: '任务看板', machines: '开发机与 agent', settings: '设置' };
  if (currentView !== 'workbench') {
    return (
      <footer className="status-bar">
        <span><StatusDot />agentd 已连接</span><span>3 台开发机</span><span>9 个 handoff 任务</span>
        <span className="status-spacer" />
        <span>{globalLabels[currentView]}</span><span>2026-08-09&nbsp;&nbsp;14:26</span>
      </footer>
    );
  }
  return (
    <footer className="status-bar">
      <span><GitBranch size={13} />feat/droppeer-race-fix*</span>
      <span>Go 1.24.5</span>
      <span><TriangleAlert size={13} />0</span>
      <span className="status-spacer" />
      <span>行 78, 列 1</span><span>制表符长度: 4</span><span>UTF-8</span><span>LF</span><span>Go</span>
      <span><StatusDot />devbox-01</span><span>{selectedDirectory}</span><span>2026-08-09&nbsp;&nbsp;14:26</span>
    </footer>
  );
}

export function App() {
  const [currentView, setCurrentView] = useState('workbench');
  const [projects, setProjects] = useState(projectRows);
  const [selectedDirectory, setSelectedDirectory] = useState('integration/b2-b3');
  const [activeTask, setActiveTask] = useState('running');
  const [selectedFile, setSelectedFile] = useState('transport_test.go');
  const openNewTerminal = () => setActiveTask(null);
  const openDirectory = (directory) => {
    setSelectedDirectory(directory);
    setActiveTask(null);
    setCurrentView('workbench');
  };
  const openTask = (task, directory = 'integration/b2-b3') => {
    setSelectedDirectory(directory);
    setActiveTask(task);
    setCurrentView('workbench');
  };
  const addProject = (project) => setProjects((current) => current.some((item) => item.id === project.id) ? current : [...current, project]);

  return (
    <div className={`app-shell ${currentView === 'workbench' ? '' : 'global-view'}`}>
      <LeftSidebar projects={projects} currentView={currentView} onNavigate={setCurrentView} selectedDirectory={selectedDirectory} onSelectDirectory={openDirectory} activeTask={activeTask} onSelectTask={openTask} onAddProject={addProject} />
      {currentView === 'workbench' ? (
        <>
          <section className="center-shell">
            <ContextBar selectedDirectory={selectedDirectory} onNewTerminal={openNewTerminal} />
            <Workspace activeTask={activeTask} setActiveTask={setActiveTask} selectedFile={selectedFile} setSelectedFile={setSelectedFile} />
          </section>
          <FileTree selectedDirectory={selectedDirectory} selectedFile={selectedFile} setSelectedFile={setSelectedFile} />
        </>
      ) : (
        <main className="global-shell">
          {currentView === 'board' && <TaskBoardPage onOpenTask={openTask} />}
          {currentView === 'machines' && <MachinesPage onOpenDirectory={openDirectory} />}
          {currentView === 'settings' && <SettingsPage />}
        </main>
      )}
      <StatusBar selectedDirectory={selectedDirectory} currentView={currentView} />
    </div>
  );
}
