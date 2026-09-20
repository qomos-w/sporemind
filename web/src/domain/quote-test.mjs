function quoteIfNeeded(s) {
  if (s === '' || s.includes(':') || s.includes(',') || s.includes('"')) {
    if (s.includes("'")) {
      return `"${s.replace(/"/g, '\\"')}"`
    }
    return `'${s}'`
  }
  return s
}

const json = JSON.stringify([{ action: 'pause', target: 'agent:coder' }])
console.log('JSON string:', json)
console.log('Quoted:', quoteIfNeeded(json))

// Simulate backend parseScalarValue: strip outer single quotes
function backendParseScalar(value) {
  if (value.length >= 2 && ((value[0] === "'" && value[value.length-1] === "'") || (value[0] === '"' && value[value.length-1] === '"'))) {
    return value.slice(1, -1)
  }
  return value
}

const quoted = quoteIfNeeded(json)
const backendParsed = backendParseScalar(quoted)
console.log('Backend parsed:', backendParsed)
console.log('JSON.parse works:', JSON.stringify(JSON.parse(backendParsed)))

// Now test the OLD double-quote approach
function oldQuoteIfNeeded(s) {
  if (s === '' || s.includes(':') || s.includes(',') || s.includes('"')) {
    return `"${s.replace(/"/g, '\\"')}"`
  }
  return s
}

const oldQuoted = oldQuoteIfNeeded(json)
console.log('\nOLD quoted:', oldQuoted)
const oldBackendParsed = backendParseScalar(oldQuoted)
console.log('OLD backend parsed:', oldBackendParsed)
try {
  console.log('OLD JSON.parse works:', JSON.stringify(JSON.parse(oldBackendParsed)))
} catch (e) {
  console.log('OLD JSON.parse FAILED:', e.message)
}
