(function () {
  if (window.__siwuPickerReady) return
  window.__siwuPickerReady = true

  var box = document.createElement('div')
  box.style.cssText = [
    'position:fixed', 'z-index:2147483647', 'pointer-events:none',
    'border:2px solid #7f77dd', 'background:rgba(127,119,221,.16)',
    'border-radius:2px', 'transition:all .06s linear', 'display:none'
  ].join(';')
  var label = document.createElement('div')
  label.style.cssText = [
    'position:absolute', 'left:0', 'top:-22px', 'max-width:60vw',
    'padding:1px 6px', 'font:11px/18px ui-monospace,SFMono-Regular,Menlo,monospace',
    'color:#fff', 'background:#534ab7', 'border-radius:3px',
    'white-space:nowrap', 'overflow:hidden', 'text-overflow:ellipsis'
  ].join(';')
  box.appendChild(label)

  function ready() {
    document.documentElement.appendChild(box)
  }
  if (document.body) ready()
  else document.addEventListener('DOMContentLoaded', ready)

  var current = null

  function draw(el) {
    if (!el || !el.getBoundingClientRect) return
    var r = el.getBoundingClientRect()
    box.style.display = 'block'
    box.style.left = r.left + 'px'
    box.style.top = r.top + 'px'
    box.style.width = r.width + 'px'
    box.style.height = r.height + 'px'
    label.textContent = describe(el)
  }

  function describe(el) {
    var s = el.tagName.toLowerCase()
    if (el.id) s += '#' + el.id
    if (el.classList && el.classList.length) s += '.' + el.classList[0]
    return s + '  ·  ' + (el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 40)
  }

  function esc(v) {
    return String(v).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
  }

  function xpathOf(el) {
    if (!el || el.nodeType !== 1) return ''
    if (el.id) return "//*[@id='" + el.id + "']"
    var parts = []
    var cur = el
    while (cur && cur.nodeType === 1) {
      var i = 1
      var sib = cur.previousElementSibling
      while (sib) {
        if (sib.tagName === cur.tagName) i++
        sib = sib.previousElementSibling
      }
      var cls = cur.classList && cur.classList.length ? cur.classList[0] : ''
      var seg = cur.tagName.toLowerCase()
      if (cls) seg += "[contains(@class,'" + cls + "')]"
      else seg += '[' + i + ']'
      parts.unshift(seg)
      if (cur.id) {
        parts.unshift("*[@id='" + cur.id + "']")
        break
      }
      cur = cur.parentElement
    }
    var p = parts.join('/')
    return p.charAt(0) === '/' ? p : '/' + p
  }

  function cssOf(el) {
    var parts = []
    var cur = el
    while (cur && cur.nodeType === 1 && cur !== document.documentElement) {
      var seg = cur.tagName.toLowerCase()
      if (cur.id) {
        parts.unshift('#' + cur.id)
        break
      }
      if (cur.classList && cur.classList.length) seg += '.' + cur.classList[0]
      parts.unshift(seg)
      cur = cur.parentElement
    }
    return parts.join(' > ')
  }

  function payload(el) {
    var text = (el.textContent || '').replace(/\s+/g, ' ').trim()
    return {
      source: 'siwu-picker',
      type: 'picked',
      tag: el.tagName.toLowerCase(),
      xpath: xpathOf(el),
      css: cssOf(el),
      text: text.slice(0, 200),
      href: el.getAttribute && el.getAttribute('href') || '',
      count: null
    }
  }

  function post(msg) {
    try {
      parent.postMessage(msg, '*')
    } catch (e) { /* 忽略：父页面已关闭 */ }
  }

  document.addEventListener('mousemove', function (e) {
    if (e.target && e.target !== current) {
      current = e.target
      draw(current)
    }
  }, true)

  document.addEventListener('mouseleave', function () {
    box.style.display = 'none'
  }, true)

  document.addEventListener('click', function (e) {
    e.preventDefault()
    e.stopPropagation()
    var el = e.target
    if (!el || el === box) return
    draw(el)
    post(payload(el))
  }, true)

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      box.style.display = 'none'
      post({ source: 'siwu-picker', type: 'cleared' })
      return
    }
    if (!current) return
    if (e.key === 'ArrowUp' && current.parentElement) {
      e.preventDefault()
      current = current.parentElement
      draw(current)
      post(payload(current))
    }
    if (e.key === 'ArrowDown' && current.firstElementChild) {
      e.preventDefault()
      current = current.firstElementChild
      draw(current)
      post(payload(current))
    }
  }, true)

  // 父页面主动要求高亮某个 xpath（例如从候选列表点回来）
  window.addEventListener('message', function (e) {
    var d = e.data
    if (!d || d.source !== 'siwu-host') return
    if (d.type === 'highlight' && d.xpath) {
      try {
        var r = document.evaluate(d.xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null)
        if (r.singleNodeValue) {
          current = r.singleNodeValue
          draw(current)
        }
      } catch (err) { /* xpath 非法时忽略 */ }
    }
    if (d.type === 'clear') {
      box.style.display = 'none'
      current = null
    }
  })

  post({ source: 'siwu-picker', type: 'ready', href: location.href })
})()
