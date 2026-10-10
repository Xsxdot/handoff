// Real workspace projection guards: card association and directory identity, not machine proximity.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { ProjectNode } from '../../api/types'
import type { SessionSummary } from '../../api/rooms'
import { MobileWorkspace } from './MobileWorkspace'

const project = { project_id: 'p', name: 'handoff', locations: [{ machine: '', name: 'handoff', path: '/repo', workspaces: [{ path: '/repo', branch: 'main', is_main: true }] }] } as ProjectNode
const room = (id: string, cards: string[]): SessionSummary => ({ id, kind:'session', title:id, owner:'user:a', archived:false, unread:0, needs_human:false, last_activity:'', cards:cards.map(card_id => ({card_id})) })
const props = () => ({ projects:[project], machines:[], tasks:[], sessions:[room('same-project',['B1']),room('cross-project',['B1','B2']),room('unmapped',['missing'])], projectOfCard:(id:string)=>id==='B1'?'handoff':id==='B2'?'other':'', openedItems:[], ptySessions:[], needsCount:2, ready:true, errorText:'', expired:false, onOpenSession:vi.fn(), onOpenItem:vi.fn(), onRestoreTerminal:vi.fn(), onOpenTerminal:vi.fn(), onOpenDirectory:vi.fn(), onOpenProject:vi.fn(), onEditProject:vi.fn(), onAddProject:vi.fn(), onOpenSessions:vi.fn(), onOpenCards:vi.fn() })
describe('MobileWorkspace',()=>{
 it('toggles expand from the project name; detail opens via ⋯ sheet 「工作树与项目目录」',()=>{
  const input={...props(),sessions:[]}; render(<MobileWorkspace {...input}/>);
  // Empty project lives in 其余 bucket until opened
  fireEvent.click(screen.getByRole('button',{name:'展开 其余 1 个项目'}))
  // Decision A: name toggles expand, does not open project detail
  expect(screen.getByRole('button',{name:'展开 handoff'})).toHaveAttribute('aria-expanded','false')
  fireEvent.click(screen.getByTestId('mobile-project-card'))
  expect(input.onOpenProject).not.toHaveBeenCalled()
  expect(screen.getByRole('button',{name:'收起 handoff'})).toHaveAttribute('aria-expanded','true')
  // ⋯ opens action sheet; detail path is sheet item
  fireEvent.click(screen.getByRole('button',{name:'handoff 操作'}))
  expect(screen.getByRole('dialog',{name:'handoff 操作与位置'})).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button',{name:'工作树与项目目录'}))
  expect(input.onOpenProject).toHaveBeenCalledWith('p')
 })
 it('groups by linked cards; cross-project and unknown association never invent a project',()=>{
  render(<MobileWorkspace {...props()}/>); expect(screen.getByText('same-project')).toBeInTheDocument(); fireEvent.click(screen.getByRole('button',{name:'展开 跨项目协作'})); expect(screen.getByText('cross-project')).toBeInTheDocument(); fireEvent.click(screen.getByRole('button',{name:'展开 其他工作'})); expect(screen.getByText('unmapped')).toBeInTheDocument()
 })
 it('does not expose another project terminal just because machine matches',()=>{
  render(<MobileWorkspace {...props()} ptySessions={[{id:'foreign',machine:'',base_kind:'workspace',base_path:'/other',shell:'bash'}, {id:'mine',machine:'',base_kind:'workspace',base_path:'/repo',shell:'zsh'}] as never}/>); expect(screen.getByText('zsh · main')).toBeInTheDocument(); expect(screen.queryByText('bash · main')).not.toBeInTheDocument()
 })
 it('blocks creation offline but preserves cached resources and error reason',()=>{
  render(<MobileWorkspace {...props()} ready={false} errorText="network unavailable"/>); fireEvent.click(screen.getByRole('button',{name:'工作台操作'})); expect(screen.getByRole('button',{name:'添加项目'})).toBeDisabled(); expect(screen.getByText('same-project')).toBeInTheDocument(); expect(screen.getByText('network unavailable')).toBeInTheDocument()
 })
 it('searches and expands a project when only an opened file or terminal matches',()=>{
  const input={...props(),sessions:[],openedItems:[{tabId:'f',groupId:'g',label:'README.md',base:{key:'repo',kind:'workspace',path:'/repo',label:'repo',projectName:'handoff',machine:''},content:{kind:'file',rel:'README.md'}}] as never};
  render(<MobileWorkspace {...input}/>); fireEvent.click(screen.getByRole('button',{name:'搜索工作台'})); fireEvent.change(screen.getByRole('textbox',{name:'搜索项目与资源'}),{target:{value:'README'}});
  expect(screen.getByRole('button',{name:'收起 handoff'})).toHaveAttribute('aria-expanded','true'); expect(screen.getByText('README.md')).toBeInTheDocument()
 })
 it('filters terminal rows by the search query and matches terminal paths',()=>{
  render(<MobileWorkspace {...props()} ptySessions={[{id:'match',machine:'',base_kind:'workspace',base_path:'/repo',shell:'zsh'}, {id:'miss',machine:'',base_kind:'workspace',base_path:'/repo',shell:'bash'}] as never}/>);
  fireEvent.click(screen.getByRole('button',{name:'搜索工作台'})); fireEvent.change(screen.getByRole('textbox',{name:'搜索项目与资源'}),{target:{value:'zsh'}});
  expect(screen.getByText('zsh · main')).toBeInTheDocument(); expect(screen.queryByText('bash · main')).not.toBeInTheDocument()
 })
 it('searches an already-open terminal by its live shell even when its tab label is generic',()=>{
  const input={...props(),sessions:[],openedItems:[{tabId:'t',groupId:'g',label:'终端',base:{key:'repo',kind:'workspace',path:'/repo',label:'repo',projectName:'handoff',machine:''},content:{kind:'terminal',sessionId:'live'}}] as never,ptySessions:[{id:'live',machine:'',base_kind:'workspace',base_path:'/repo',shell:'zsh'}] as never};
  render(<MobileWorkspace {...input}/>); fireEvent.click(screen.getByRole('button',{name:'搜索工作台'})); fireEvent.change(screen.getByRole('textbox',{name:'搜索项目与资源'}),{target:{value:'zsh'}});
  expect(screen.getByText('终端')).toBeInTheDocument()
 })
 it('shows live home and unmatched terminals under other work without assigning them to a project',()=>{
  const sessions=[{id:'home',machine:'',base_kind:'home',base_path:'/Users/a',shell:'zsh',exit_code:undefined},{id:'scratch',machine:'',base_kind:'scratch',base_path:'/tmp',shell:'bash',exit_code:undefined},{id:'unknown',machine:'x',base_kind:'workspace',base_path:'/unmatched',shell:'fish',exit_code:undefined}] as never;
  render(<MobileWorkspace {...props()} sessions={[]} ptySessions={sessions}/>);
  fireEvent.click(screen.getByRole('button',{name:'展开 其他工作'})); expect(screen.getByText(/zsh ·/)).toBeInTheDocument(); expect(screen.getByText(/bash ·/)).toBeInTheDocument(); expect(screen.getByText(/fish ·/)).toBeInTheDocument(); expect(screen.queryByText('zsh · main')).not.toBeInTheDocument()
 })
 it('does not present an unloaded needs count as zero',()=>{
  const {container}=render(<MobileWorkspace {...props()} needsCount={0} ready={false}/>); expect(container.querySelector('.mobile-count')).toBeNull()
 })
 it('buckets empty projects under 其余 N；有资源置顶且不进桶',()=>{
  const empty={ project_id:'e', name:'empty-proj', locations:[{ machine:'', name:'empty-proj', path:'/empty', workspaces:[{ path:'/empty', branch:'main', is_main:true }] }] } as ProjectNode
  const rich={...project}
  const input={...props(), projects:[empty, rich], sessions:[room('same-project',['B1'])]}
  const {container}=render(<MobileWorkspace {...input}/>);
  expect(screen.getByTestId('mobile-empty-project-bucket')).toBeInTheDocument()
  expect(screen.getByRole('button',{name:'展开 其余 1 个项目'})).toHaveAttribute('aria-expanded','false')
  expect(screen.queryByText('empty-proj')).not.toBeInTheDocument()
  const topGroups=[...container.querySelectorAll('.mobile-workspace > .mobile-project-group:not(.mobile-empty-bucket)')]
  expect(topGroups[0].querySelector('b')?.textContent).toBe('handoff')
  expect(topGroups[0].classList.contains('is-empty-collapsed')).toBe(false)
  fireEvent.click(screen.getByRole('button',{name:'展开 其余 1 个项目'}))
  expect(screen.getByText('empty-proj')).toBeInTheDocument()
  const emptyGroup=[...container.querySelectorAll('.mobile-empty-bucket-body .mobile-project-group')].find(g=>g.querySelector('b')?.textContent==='empty-proj')!
  expect(emptyGroup.classList.contains('is-empty-collapsed')).toBe(true)
 })
 it('hides needs badge and weakens row when count is zero; still tappable',()=>{
  const input={...props(), needsCount:0}
  const {container}=render(<MobileWorkspace {...input}/>);
  expect(container.querySelector('.mobile-count')).toBeNull()
  expect(container.querySelector('.mobile-attention-row')!.classList.contains('is-weak')).toBe(true)
  fireEvent.click(screen.getByRole('button',{name:'需要你处理'}))
  expect(input.onOpenCards).toHaveBeenCalled()
 })
 it('shows bright needs count when >0',()=>{
  render(<MobileWorkspace {...props()}/>);
  expect(screen.getByText('2')).toBeInTheDocument()
  expect(document.querySelector('.mobile-count.is-bright')).toBeInTheDocument()
  expect(document.querySelector('.mobile-attention-row')!.classList.contains('is-weak')).toBe(false)
 })
 it('action sheet uses two primary cards with black CTAs and weak secondary list',()=>{
  const input={...props(),sessions:[]}; render(<MobileWorkspace {...input}/>);
  fireEvent.click(screen.getByRole('button',{name:'展开 其余 1 个项目'}))
  fireEvent.click(screen.getByRole('button',{name:'handoff 操作'}))
  const dialog=screen.getByRole('dialog',{name:'handoff 操作与位置'})
  expect(dialog.querySelectorAll('.mobile-sheet-group-primary')).toHaveLength(2)
  expect(dialog.querySelectorAll('.mobile-sheet-action.primary')).toHaveLength(2)
  expect(screen.getByRole('button',{name:'新建终端'})).toHaveClass('primary')
  expect(screen.getByRole('button',{name:'浏览目录'})).toHaveClass('primary')
  expect(dialog.querySelector('.mobile-sheet-group-secondary')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button',{name:'工作树与项目目录'}))
  expect(input.onOpenProject).toHaveBeenCalledWith('p')
 })
})
