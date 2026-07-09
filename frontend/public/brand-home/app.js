const nav = document.querySelector('.site-nav')
const subCount = document.querySelector('#subCount')
const subCountText = document.querySelector('#subCountText')
const savingValue = document.querySelector('#savingValue')

function updateNavElevation() {
  if (!nav) return
  nav.dataset.elevated = window.scrollY > 16 ? 'true' : 'false'
}

function updateSavings() {
  if (!subCount || !subCountText || !savingValue) return
  const count = Number(subCount.value)
  const estimatedFixedCost = count * 20
  const suggestedReserve = count <= 2 ? 12 : count <= 4 ? 20 : 32
  const saving = Math.max(0, estimatedFixedCost - suggestedReserve)
  subCountText.textContent = `${count} 个`
  savingValue.textContent = `$${saving}/月`
}

window.addEventListener('scroll', updateNavElevation, { passive: true })
subCount?.addEventListener('input', updateSavings)

updateNavElevation()
updateSavings()
