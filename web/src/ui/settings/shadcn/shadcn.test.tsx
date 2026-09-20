import '@testing-library/jest-dom/vitest'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { userEvent } from '@testing-library/user-event'

// Polyfill for Base UI's animation frames (normally provided by test-setup.ts)
globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0) as unknown as number
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)

import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from './ui/card'
import { Field, FieldLabel, FieldDescription, FieldError } from './ui/field'
import { Input } from './ui/input'
import { Label } from './ui/label'
import { RadioGroup, RadioGroupItem } from './ui/radio-group'
import { SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from './ui/select'
import { Separator } from './ui/separator'
import { Slider } from './ui/slider'
import { Switch } from './ui/switch'
import { Textarea } from './ui/textarea'
import { Checkbox } from './ui/checkbox'
import { TooltipProvider, TooltipRoot, TooltipTrigger, TooltipContent } from './ui/tooltip'
import { TabsRoot, TabsList, TabsTrigger, TabsContent } from './ui/tabs'

// ---- data-guide-id query locator (mirrors ui-interactor.ts) ----
function q(guideId: string) {
  return document.querySelector(`[data-guide-id="${guideId}"]`)
}

// =============================================================================
// 1. Badge
// =============================================================================
describe('Badge', () => {
  it('renders with default variant', () => {
    render(<Badge data-guide-id="badge.1">Default</Badge>)
    expect(q('badge.1')).toBeInTheDocument()
    expect(q('badge.1')).toHaveTextContent('Default')
  })

  it('renders with variant classes', () => {
    const { container } = render(<Badge variant="destructive">Error</Badge>)
    expect(container.firstChild).toHaveClass('bg-destructive/10')
  })
})

// =============================================================================
// 2. Button
// =============================================================================
describe('Button', () => {
  it('renders and fires onClick', async () => {
    const fn = vi.fn()
    render(<Button data-guide-id="btn.1" onClick={fn}>Click</Button>)
    expect(q('btn.1')).toBeInTheDocument()
    await userEvent.click(q('btn.1')!)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('renders with variant and size', () => {
    const { container } = render(<Button variant="destructive" size="lg">Delete</Button>)
    expect(container.firstChild).toHaveClass('bg-destructive/10')
    expect(container.firstChild).toHaveClass('h-9')
  })
})

// =============================================================================
// 3. Card
// =============================================================================
describe('Card', () => {
  it('renders card with all subcomponents', () => {
    render(
      <Card data-guide-id="card.1">
        <CardHeader>
          <CardTitle>Title</CardTitle>
          <CardDescription>Desc</CardDescription>
        </CardHeader>
        <CardContent>Body</CardContent>
        <CardFooter>Footer</CardFooter>
      </Card>,
    )
    expect(q('card.1')).toBeInTheDocument()
    expect(screen.getByText('Title')).toBeInTheDocument()
    expect(screen.getByText('Desc')).toBeInTheDocument()
    expect(screen.getByText('Body')).toBeInTheDocument()
    expect(screen.getByText('Footer')).toBeInTheDocument()
  })
})

// =============================================================================
// 4. Field
// =============================================================================
describe('Field', () => {
  it('renders Field with label, description, error', () => {
    render(
      <Field data-guide-id="field.1">
        <FieldLabel>Name</FieldLabel>
        <FieldDescription>Enter your name</FieldDescription>
        <FieldError>Required</FieldError>
      </Field>,
    )
    expect(q('field.1')).toBeInTheDocument()
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Enter your name')).toBeInTheDocument()
    expect(screen.getByText('Required')).toBeInTheDocument()
  })
})

// =============================================================================
// 5. Input
// =============================================================================
describe('Input', () => {
  it('renders and accepts value via native setter path', async () => {
    const onChange = vi.fn()
    render(<Input data-guide-id="input.1" onChange={onChange} />)
    const el = q('input.1') as HTMLInputElement
    expect(el).toBeInTheDocument()

    // Simulate ui-interactor.ts native setter + dispatchEvent path
    const nativeSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    if (nativeSetter) {
      nativeSetter.call(el, 'hello')
    } else {
      el.value = 'hello'
    }
    el.dispatchEvent(new Event('input', { bubbles: true }))
    expect(onChange).toHaveBeenCalled()
    expect(el.value).toBe('hello')
  })

  it('renders with placeholder', () => {
    render(<Input placeholder="Type here" />)
    expect(screen.getByPlaceholderText('Type here')).toBeInTheDocument()
  })
})

// =============================================================================
// 6. Label
// =============================================================================
describe('Label', () => {
  it('renders as label element', () => {
    render(<Label data-guide-id="label.1" htmlFor="x">Name</Label>)
    const el = q('label.1')
    expect(el).toBeInTheDocument()
    expect(el).toHaveAttribute('for', 'x')
    expect(el).toHaveAttribute('data-slot', 'label')
  })
})

// =============================================================================
// 7. RadioGroup
// =============================================================================
describe('RadioGroup', () => {
  it('renders and selects on click', async () => {
    const fn = vi.fn()
    render(
      <RadioGroup data-guide-id="rg.1" onValueChange={fn}>
        <RadioGroupItem value="a" data-guide-id="rg.a" />
        <RadioGroupItem value="b" data-guide-id="rg.b" />
      </RadioGroup>,
    )
    expect(q('rg.1')).toBeInTheDocument()
    expect(q('rg.a')).toBeInTheDocument()
    await userEvent.click(q('rg.a')!)
    expect(fn).toHaveBeenCalledWith('a', expect.anything())
  })

  it('renders with defaultValue', () => {
    const fn = vi.fn()
    render(
      <RadioGroup defaultValue="a" onValueChange={fn}>
        <RadioGroupItem value="a" />
        <RadioGroupItem value="b" />
      </RadioGroup>,
    )
    // defaultValue="a" sets initial selection — no crash
  })
})

// =============================================================================
// 8. Select
// =============================================================================
describe('Select', () => {
  it('opens popup and selects item', async () => {
    const fn = vi.fn()
    render(
      <SelectRoot onValueChange={fn}>
        <SelectTrigger data-guide-id="sel.trigger">
          <SelectValue placeholder="Pick" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="x" data-guide-id="sel.item-x">
            <SelectItemText>Option X</SelectItemText>
          </SelectItem>
          <SelectItem value="y">
            <SelectItemText>Option Y</SelectItemText>
          </SelectItem>
        </SelectContent>
      </SelectRoot>,
    )
    // SelectRoot renders no DOM; the interactive anchor is the trigger
    expect(q('sel.trigger')).toBeInTheDocument()

    // Click trigger to open
    await userEvent.click(q('sel.trigger')!)
    // Click the first item
    const itemX = q('sel.item-x')
    expect(itemX).toBeInTheDocument()
    await userEvent.click(itemX!)
    expect(fn).toHaveBeenCalled()
  })
})

// =============================================================================
// 9. Separator
// =============================================================================
describe('Separator', () => {
  it('renders horizontal separator', () => {
    render(<Separator data-guide-id="sep.1" />)
    expect(q('sep.1')).toBeInTheDocument()
    expect(q('sep.1')).toHaveAttribute('data-slot', 'separator')
  })

  it('renders vertical separator', () => {
    const { container } = render(<Separator orientation="vertical" />)
    expect(container.firstChild).toHaveClass('data-[orientation=vertical]:self-stretch')
  })
})

// =============================================================================
// 10. Slider
// =============================================================================
describe('Slider', () => {
  it('renders with default value', () => {
    render(<Slider data-guide-id="slider.1" defaultValue={50} />)
    expect(q('slider.1')).toBeInTheDocument()
  })
})

// =============================================================================
// 11. Switch
// =============================================================================
describe('Switch', () => {
  it('toggles on click', async () => {
    const fn = vi.fn()
    render(<Switch data-guide-id="switch.1" onCheckedChange={fn} />)
    const el = q('switch.1')
    expect(el).toBeInTheDocument()
    await userEvent.click(el!)
    expect(fn).toHaveBeenCalledWith(true, expect.anything())
  })

  it('renders with defaultChecked', () => {
    const fn = vi.fn()
    render(<Switch defaultChecked onCheckedChange={fn} />)
    // no crash
  })
})

// =============================================================================
// 12. Textarea
// =============================================================================
describe('Textarea', () => {
  it('renders and accepts value via native setter path', async () => {
    const onChange = vi.fn()
    render(<Textarea data-guide-id="ta.1" onChange={onChange} />)
    const el = q('ta.1') as HTMLTextAreaElement
    expect(el).toBeInTheDocument()

    // Simulate ui-interactor.ts native setter path for textarea
    const nativeSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    if (nativeSetter) {
      nativeSetter.call(el, 'multiline')
    } else {
      el.value = 'multiline'
    }
    el.dispatchEvent(new Event('input', { bubbles: true }))
    expect(onChange).toHaveBeenCalled()
    expect(el.value).toBe('multiline')
  })
})

// =============================================================================
// 13. Checkbox
// =============================================================================
describe('Checkbox', () => {
  it('toggles on click', async () => {
    const fn = vi.fn()
    render(<Checkbox data-guide-id="cb.1" onCheckedChange={fn} />)
    const el = q('cb.1')
    expect(el).toBeInTheDocument()
    await userEvent.click(el!)
    expect(fn).toHaveBeenCalledWith(true, expect.anything())
  })

  it('renders with defaultChecked', () => {
    const fn = vi.fn()
    render(<Checkbox defaultChecked onCheckedChange={fn} />)
    // no crash
  })
})

// =============================================================================
// 14. Tooltip
// =============================================================================
describe('Tooltip', () => {
  it('shows tooltip on hover', async () => {
    render(
      <TooltipProvider>
        <TooltipRoot>
          <TooltipTrigger data-guide-id="tt.trigger" delay={0}>
            Hover me
          </TooltipTrigger>
          <TooltipContent>Tooltip content</TooltipContent>
        </TooltipRoot>
      </TooltipProvider>,
    )
    expect(q('tt.trigger')).toBeInTheDocument()

    // Hover the trigger
    await userEvent.hover(q('tt.trigger')!)
    // Tooltip content should appear
    expect(await screen.findByText('Tooltip content')).toBeInTheDocument()
  })
})

// =============================================================================
// 15. Tabs
// =============================================================================
describe('Tabs', () => {
  it('switches panel on tab click', async () => {
    const fn = vi.fn()
    render(
      <TabsRoot defaultValue="a" data-guide-id="tabs.1" onValueChange={fn}>
        <TabsList>
          <TabsTrigger value="a" data-guide-id="tabs.tab-a">Tab A</TabsTrigger>
          <TabsTrigger value="b" data-guide-id="tabs.tab-b">Tab B</TabsTrigger>
        </TabsList>
        <TabsContent value="a">Content A</TabsContent>
        <TabsContent value="b">Content B</TabsContent>
      </TabsRoot>,
    )
    expect(q('tabs.1')).toBeInTheDocument()
    expect(screen.getByText('Content A')).toBeInTheDocument()

    // Click Tab B
    await userEvent.click(q('tabs.tab-b')!)
    expect(fn).toHaveBeenCalled()
  })
})

// =============================================================================
// 16. All exports from index
// =============================================================================
describe('index exports', () => {
  it('exports all components', async () => {
    const mod = await import('./ui/index')
    expect(mod.Badge).toBeDefined()
    expect(mod.Button).toBeDefined()
    expect(mod.Card).toBeDefined()
    expect(mod.CardHeader).toBeDefined()
    expect(mod.Field).toBeDefined()
    expect(mod.Input).toBeDefined()
    expect(mod.Label).toBeDefined()
    expect(mod.RadioGroup).toBeDefined()
    expect(mod.RadioGroupItem).toBeDefined()
    expect(mod.SelectRoot).toBeDefined()
    expect(mod.Separator).toBeDefined()
    expect(mod.Slider).toBeDefined()
    expect(mod.Switch).toBeDefined()
    expect(mod.Textarea).toBeDefined()
    expect(mod.Checkbox).toBeDefined()
    expect(mod.TooltipProvider).toBeDefined()
    expect(mod.TooltipRoot).toBeDefined()
    expect(mod.TooltipTrigger).toBeDefined()
    expect(mod.TooltipContent).toBeDefined()
    expect(mod.TabsRoot).toBeDefined()
    expect(mod.TabsList).toBeDefined()
    expect(mod.TabsTrigger).toBeDefined()
    expect(mod.TabsContent).toBeDefined()
  })
})