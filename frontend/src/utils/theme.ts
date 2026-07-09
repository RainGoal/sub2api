export function forceDarkTheme() {
  document.documentElement.classList.add('dark')
  localStorage.setItem('theme', 'dark')
}

