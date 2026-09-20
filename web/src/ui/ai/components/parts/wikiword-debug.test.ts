import { describe, it, expect } from 'vitest'
import { processWikiWords } from './wikiword'

describe('debug: backtick vs plain CamelCase', () => {
  it('compare outputs', () => {
    const plain = processWikiWords('see CamelCase for details')
    const backtick = processWikiWords('see `CamelCase` for details')
    
    // Extract href from both using the same regex the a-component uses
    const plainHref = plain.match(/\](wiki:[^)]+)\)/)?.[1] || 'NOT FOUND'
    const backtickHref = backtick.match(/\](wiki:[^)]+)\)/)?.[1] || 'NOT FOUND'
    
    const plainWord = decodeURIComponent(plainHref.slice(5))
    const backtickWord = decodeURIComponent(backtickHref.slice(5))
    
    console.log('PLAIN output:', JSON.stringify(plain))
    console.log('BACKTICK output:', JSON.stringify(backtick))
    console.log('PLAIN href:', plainHref, '→ word:', JSON.stringify(plainWord))
    console.log('BACKTICK href:', backtickHref, '→ word:', JSON.stringify(backtickWord))
    
    expect(plainWord).toBe(backtickWord)
  })
})
