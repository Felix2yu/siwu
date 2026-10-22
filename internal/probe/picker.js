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
    // 预览是净化后的静态 DOM，站点原本负责关闭的遮罩/浮层会永久停留。
    // 只在预览里隐藏它们，不影响抽取用的会话 HTML。
    var style = document.createElement('style')
    style.textContent = [
      // 加载态
      '.at-activity-indicator',
      '.at-loading',
      '.van-loading',
      '.ant-spin-spinning',
      '.el-loading-mask',
      '.weui-loading',
      '[class*="loading-mask"]',
      // 未激活的 tab 面板：真实页面由 JS 收起（height:0），但里面绝对定位的下拉面板
      // 会逃出裁剪、盖在列表上。司寤抓的快照里 JS 不跑，于是出现「列表在、看不见」。
      '.at-tabs-pane--inactive',
      // 日期/选项选择器：同理，快照里没人关它
      '.weui-picker'
    ].join(',') + '{display:none !important}'
    ;(document.head || document.documentElement).appendChild(style)
    // Taro 的 loading toast 没有 loading 类名，只在文案上区分
    var toasts = document.querySelectorAll('.taro__toast')
    for (var i = 0; i < toasts.length; i++) {
      if (toasts[i].textContent.indexOf('加载中') !== -1) {
        toasts[i].style.setProperty('display', 'none', 'important')
      }
    }
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

  // 把「条目选择器」解析成第一个命中节点：带前缀按前缀，裸写先当 CSS 再当 XPath
  function resolve(sel) {
    if (!sel) return null
    try {
      if (sel.indexOf('css:') === 0) return document.querySelector(sel.slice(4))
      if (sel.indexOf('xpath:') === 0) {
        var r = document.evaluate(sel.slice(6), document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null)
        return r.singleNodeValue
      }
      var el = document.querySelector(sel)
      if (el) return el
      var r2 = document.evaluate(sel, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null)
      return r2.singleNodeValue
    } catch (e) {
      return null
    }
  }

  // 滚到可视区。移动端 H5（Taro / 小程序转 H5）几乎都不是 document 在滚，
  // 而是内层某个 overflow:scroll 的容器，scrollIntoView 对它无效，只能自己找容器设 scrollTop。
  // 落点先放在容器 35% 处；再用命中测试校正——这类页面顶部常有 z-index 999 的吸顶筛选栏，
  // 贴顶会被它整块盖住（看起来就是「抽到了但页面上什么都没有」）。
  function reveal(el) {
    var sc = null
    var n = el.parentElement
    while (n && n !== document.body) {
      var cs = window.getComputedStyle(n)
      if ((cs.overflowY === 'auto' || cs.overflowY === 'scroll') && n.scrollHeight > n.clientHeight + 4) {
        sc = n
        break
      }
      n = n.parentElement
    }
    if (!sc) {
      try { el.scrollIntoView({ block: 'center', inline: 'nearest' }) } catch (e) { /* 忽略 */ }
      return
    }
    var offset = el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop
    sc.scrollTop = Math.max(0, offset - Math.round(sc.clientHeight * 0.35))

    for (var i = 0; i < 8; i++) {
      var r = el.getBoundingClientRect()
      var y = Math.round(Math.min(Math.max(r.top + 12, 1), window.innerHeight - 2))
      var x = Math.round(r.left + Math.min(30, r.width / 2))
      var hit = document.elementFromPoint(x, y)
      if (hit && (hit === el || el.contains(hit))) return
      if (r.top < 0) {
        sc.scrollTop += 60            // 条目在上方，往下滚
      } else {
        sc.scrollTop = Math.max(0, sc.scrollTop - 60) // 被吸顶栏盖住，把它往下推
      }
    }
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
    // 「定位到第一条」：移动端 H5 的列表常在首屏之外（顶部搜索/筛选占几百像素），
    // 抽到条目也看不见，所以要能把它滚进视野。
    if (d.type === 'scrollTo') {
      var el = resolve(d.sel)
      if (el) {
        current = el
        reveal(el)
        draw(el)
      }
    }
    if (d.type === 'clear') {
      box.style.display = 'none'
      current = null
    }
  })

  post({ source: 'siwu-picker', type: 'ready', href: location.href })
})()
