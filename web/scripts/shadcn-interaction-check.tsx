/**
 * Ad-hoc interaction verification for shadcn UI primitives.
 * Runs standalone (tsx) because vitest's module root resolution is broken in
 * this git-worktree environment (`/src/...` resolution) — affects all tests.
 * Mirrors the ui-interactor.ts primitives: el.click() / native-setter input.
 */
import { Window } from 'happy-dom'

// ---- happy-dom globals (subset needed by react-dom + components) ----
const win = new Window()
win.document.write('<html><head></head><body></body></html>')
const g = globalThis as Record<string, unknown>
g.window = win
g.document = win.document
Object.defineProperty(g, 'navigator', { value: win.navigator, configurable: true })
g.HTMLElement = win.HTMLElement
g.HTMLInputElement = win.HTMLInputElement
g.HTMLTextAreaElement = win.HTMLTextAreaElement
g.HTMLButtonElement = win.HTMLButtonElement
g.Event = win.Event
g.CustomEvent = win.CustomEvent
g.MouseEvent = win.MouseEvent
g.KeyboardEvent = win.KeyboardEvent
g.FocusEvent = win.FocusEvent
g.Node = win.Node
g.getComputedStyle = win.getComputedStyle.bind(win)
g.requestAnimationFrame = ((cb: FrameRequestCallback) => setTimeout(cb, 0)) as unknown as typeof requestAnimationFrame
g.cancelAnimationFrame = ((id: number) => clearTimeout(id)) as unknown as typeof cancelAnimationFrame
g.MutationObserver = win.MutationObserver
g.ShadowRoot = win.ShadowRoot
g.DOMParser = win.DOMParser
g.DOMRect = win.DOMRect
g.Element = win.Element
g.NodeList = win.NodeList

// Enable act() environment
;(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true

let passed = 0
let failed = 0

function assert(cond: boolean, msg: string) {
  if (cond) {
    passed++
    console.log(`  PASS: ${msg}`)
  } else {
    failed++
    console.error(`  FAIL: ${msg}`)
  }
}

function q(guideId: string): HTMLElement | null {
  return document.querySelector(`[data-guide-id="${guideId}"]`)
}

// Import components after DOM globals are set.
const {
  Badge,
  Button,
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardFooter,
  Field,
  FieldLabel,
  FieldDescription,
  FieldError,
  Input,
  Label,
  RadioGroup,
  RadioGroupItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
  Separator,
  Slider,
  Switch,
  Textarea,
  Checkbox,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
  TooltipContent,
  TabsRoot,
  TabsList,
  TabsTrigger,
  TabsContent,
} = await import('../src/ui/settings/shadcn/ui/index')

const React = await import('react')
const { createRoot } = await import('react-dom/client')
const act = (await import('react')).act as typeof import('react').act

async function render(el: React.ReactElement) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => {
    root.render(el)
  })
  return { host, root }
}

async function click(guideId: string) {
  const el = q(guideId)
  if (!el) {
    throw new Error(`element not found: ${guideId}`)
  }
  el.click()
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })
}

function nativeSetInput(el: HTMLElement, value: string) {
  if (el.tagName.toLowerCase() === 'input') {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    if (setter) setter.call(el, value)
    else (el as HTMLInputElement).value = value
  } else if (el.tagName.toLowerCase() === 'textarea') {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    if (setter) setter.call(el, value)
    else (el as HTMLTextAreaElement).value = value
  }
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

// ---- 1. Badge ----
await render(<Badge data-guide-id="badge.1">Default</Badge>)
assert(!!q('badge.1') && q('badge.1')!.textContent === 'Default', 'Badge renders with data-guide-id')
q('badge.1')!.remove()

// ---- 2. Button onClick ----
{
  let clicks = 0
  await render(<Button data-guide-id="btn.1" onClick={() => { clicks++ }}>Click</Button>)
  assert(clicks === 0, 'Button onClick not fired before click')
  await click('btn.1')
  assert(clicks === 1, 'Button onClick fires after el.click()')
  q('btn.1')!.remove()
}

// ---- 3. Card ----
await render(
  <Card data-guide-id="card.1">
    <CardHeader>
      <CardTitle>T</CardTitle>
      <CardDescription>D</CardDescription>
    </CardHeader>
    <CardContent>B</CardContent>
    <CardFooter>F</CardFooter>
  </Card>,
)
assert(!!q('card.1') && !!document.querySelector('[data-slot="card"]'), 'Card renders with subcomponents')
q('card.1')!.remove()

// ---- 4. Field ----
await render(
  <Field data-guide-id="field.1">
    <FieldLabel>Name</FieldLabel>
    <FieldDescription>Desc</FieldDescription>
    <FieldError match>Err</FieldError>
  </Field>,
)
assert(
  !!q('field.1') &&
    document.body.textContent?.includes('Name') &&
    document.body.textContent?.includes('Desc') &&
    document.body.textContent?.includes('Err'),
  'Field renders label/description/error',
)
q('field.1')!.remove()

// ---- 5. Input native setter path ----
{
  let changed: string | null = null
  await render(<Input data-guide-id="input.1" onChange={(e) => { changed = e.target.value }} />)
  const el = q('input.1')!
  nativeSetInput(el, 'hello')
  assert(changed === 'hello', 'Input onChange receives value via native setter + input event')
  q('input.1')!.remove()
}

// ---- 6. Label ----
await render(<Label data-guide-id="label.1" htmlFor="x">Name</Label>)
assert(!!q('label.1') && q('label.1')!.tagName === 'LABEL' && q('label.1')!.getAttribute('for') === 'x', 'Label renders <label for>')
q('label.1')!.remove()

// ---- 7. RadioGroup ----
{
  let value: string | null = null
  await render(
    <RadioGroup data-guide-id="rg.1" onValueChange={(v) => { value = v as string }}>
      <RadioGroupItem value="a" data-guide-id="rg.a" />
      <RadioGroupItem value="b" data-guide-id="rg.b" />
    </RadioGroup>,
  )
  await click('rg.a')
  assert(value === 'a', 'RadioGroup onValueChange fires after el.click()')
  q('rg.1')!.remove()
}

// ---- 8. Select ----
{
  let selected: string | null = null
  await render(
    <SelectRoot data-guide-id="sel.1" onValueChange={(v) => { selected = v as string }}>
      <SelectTrigger data-guide-id="sel.trigger">
        <SelectValue placeholder="Pick" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="x" data-guide-id="sel.item-x"><SelectItemText>Option X</SelectItemText></SelectItem>
      </SelectContent>
    </SelectRoot>,
  )
  assert(!!q('sel.trigger'), 'Select trigger renders')
  await click('sel.trigger')
  // Popup renders into a portal appended to body.
  await act(async () => { await new Promise((r) => setTimeout(r, 50)) })
  const item = q('sel.item-x')
  assert(!!item, 'Select popup opens with item after trigger click')
  if (item) {
    item.click()
    await act(async () => { await new Promise((r) => setTimeout(r, 50)) })
    assert(selected === 'x', 'Select item click sets value')
  }
  const trig = q('sel.trigger')
  if (trig) trig.remove()
}

// ---- 9. Separator ----
await render(<Separator data-guide-id="sep.1" />)
assert(!!q('sep.1') && q('sep.1')!.getAttribute('data-slot') === 'separator', 'Separator renders')
q('sep.1')!.remove()

// ---- 10. Slider ----
await render(<Slider data-guide-id="slider.1" defaultValue={50} min={0} max={100} />)
assert(!!q('slider.1'), 'Slider renders with defaultValue')
q('slider.1')!.remove()

// ---- 11. Switch ----
{
  let checked: boolean | undefined
  await render(<Switch data-guide-id="switch.1" onCheckedChange={(c) => { checked = c }} />)
  assert(checked === undefined, 'Switch starts unchecked')
  await click('switch.1')
  assert(checked === true, 'Switch el.click() toggles to checked')
  await click('switch.1')
  assert(checked === false, 'Switch el.click() toggles back to unchecked')
  q('switch.1')!.remove()
}

// ---- 12. Textarea native setter path ----
{
  let changed: string | null = null
  await render(<Textarea data-guide-id="ta.1" onChange={(e) => { changed = e.target.value }} />)
  const el = q('ta.1')!
  nativeSetInput(el, 'multiline')
  assert(changed === 'multiline', 'Textarea onChange receives value via native setter + input event')
  q('ta.1')!.remove()
}

// ---- 13. Checkbox ----
{
  let checked: boolean | undefined
  await render(<Checkbox data-guide-id="cb.1" onCheckedChange={(c) => { checked = c }} />)
  await click('cb.1')
  assert(checked === true, 'Checkbox el.click() toggles to checked')
  q('cb.1')!.remove()
}

// ---- 14. Tooltip ----
{
  await render(
    <TooltipProvider>
      <TooltipRoot>
        <TooltipTrigger data-guide-id="tt.trigger" delay={0}>Hover me</TooltipTrigger>
        <TooltipContent>Tooltip content</TooltipContent>
      </TooltipRoot>
    </TooltipProvider>,
  )
  assert(!!q('tt.trigger'), 'Tooltip trigger renders')
  const trigger = q('tt.trigger')!
  // useHoverReferenceInteraction attaches native mouseenter/mouseleave listeners
  trigger.dispatchEvent(new MouseEvent('mouseenter', { bubbles: false }))
  await act(async () => { await new Promise((r) => setTimeout(r, 100)) })
  const popup = [...document.querySelectorAll('div')].find((n) => n.textContent === 'Tooltip content')
  assert(!!popup, 'Tooltip content appears after hover (mouseenter)')
  q('tt.trigger')!.remove()
}

// ---- 15. Tabs ----
{
  let value: string | null = null
  await render(
    <TabsRoot defaultValue="a" data-guide-id="tabs.1" onValueChange={(v) => { value = v as string }}>
      <TabsList>
        <TabsTrigger value="a" data-guide-id="tabs.tab-a">Tab A</TabsTrigger>
        <TabsTrigger value="b" data-guide-id="tabs.tab-b">Tab B</TabsTrigger>
      </TabsList>
      <TabsContent value="a">Content A</TabsContent>
      <TabsContent value="b">Content B</TabsContent>
    </TabsRoot>,
  )
  await click('tabs.tab-b')
  assert(value === 'b', 'Tabs el.click() switches active tab')
  q('tabs.1')!.remove()
}

console.log(`\n--- summary: ${passed} passed, ${failed} failed ---`)
process.exit(failed > 0 ? 1 : 0)