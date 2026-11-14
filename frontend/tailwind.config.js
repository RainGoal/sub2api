import colors from 'tailwindcss/colors.js'
import defaultTheme from 'tailwindcss/defaultTheme.js'

// The variables exist only inside the admin brand scope. Public/user routes
// retain the original palette, including Tailwind's opacity modifiers.
function brandPalette(name, palette) {
  return Object.fromEntries(Object.entries(palette).map(([shade, hex]) => {
    const rgb = hex.slice(1).match(/.{2}/g).map((part) => parseInt(part, 16)).join(' ')
    return [shade, `rgb(var(--ws-color-${name}-${shade}, ${rgb}) / <alpha-value>)`]
  }))
}

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - Teal/Cyan 青色系
        primary: brandPalette('primary', {
          50: '#f0fdfa',
          100: '#ccfbf1',
          200: '#99f6e4',
          300: '#5eead4',
          400: '#2dd4bf',
          500: '#14b8a6',
          600: '#0d9488',
          700: '#0f766e',
          800: '#115e59',
          900: '#134e4a',
          950: '#042f2e'
        }),
        // 辅助色 - 深蓝灰
        accent: brandPalette('dark', {
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617'
        }),
        // 深色模式背景
        dark: brandPalette('dark', {
          50: '#f8fafc',
          100: '#f1f5f9',
          200: '#e2e8f0',
          300: '#cbd5e1',
          400: '#94a3b8',
          500: '#64748b',
          600: '#475569',
          700: '#334155',
          800: '#1e293b',
          900: '#0f172a',
          950: '#020617'
        }),
        gray: brandPalette('gray', colors.gray)
      },
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        ...Object.fromEntries(['sm', 'DEFAULT', 'md'].map((size) => [
          size, `var(--ws-panel-shadow, ${defaultTheme.boxShadow[size]})`
        ])),
        glass: 'var(--ws-panel-shadow, 0 8px 32px rgba(0, 0, 0, 0.08))',
        'glass-sm': 'var(--ws-panel-shadow, 0 4px 16px rgba(0, 0, 0, 0.06))',
        glow: 'var(--ws-panel-shadow, 0 0 20px rgba(20, 184, 166, 0.25))',
        'glow-lg': 'var(--ws-panel-shadow, 0 0 40px rgba(20, 184, 166, 0.35))',
        card: 'var(--ws-panel-shadow, 0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06))',
        'card-hover': 'var(--ws-panel-shadow, 0 10px 40px rgba(0, 0, 0, 0.08))',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #14b8a6 0%, #0d9488 100%)',
        'gradient-dark': 'linear-gradient(135deg, #1e293b 0%, #0f172a 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(20, 184, 166, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(6, 182, 212, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(20, 184, 166, 0.08) 0px, transparent 50%)'
      },
      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(20, 184, 166, 0.25)' },
          '100%': { boxShadow: '0 0 30px rgba(20, 184, 166, 0.4)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        lg: 'var(--ws-radius-control, 0.5rem)',
        xl: 'var(--ws-radius-card, 0.75rem)',
        '2xl': 'var(--ws-radius-card, 1rem)',
        '3xl': 'var(--ws-radius-modal, 1.5rem)',
        '4xl': 'var(--ws-radius-modal, 2rem)'
      }
    }
  },
  plugins: []
}
