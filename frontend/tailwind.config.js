/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - Blue Archive 蓝白主题
        primary: {
          50: '#F6FCFF',
          100: '#DDF4FC',
          200: '#BFEAFF',
          300: '#8BDDF8',
          400: '#4DD4F6',
          500: '#00D2FF',
          600: '#12A7E8',
          700: '#0B8FD8',
          800: '#176F9E',
          900: '#2D4F68',
          950: '#071A2A'
        },
        // 辅助色 - 冰白到品牌深蓝
        accent: {
          50: '#FFFFFF',
          100: '#EAF8FE',
          200: '#CDEFFD',
          300: '#9BDEFA',
          400: '#5BCDF2',
          500: '#1598D8',
          600: '#0B8FD8',
          700: '#176F9E',
          800: '#2D4F68',
          900: '#21465E',
          950: '#071A2A'
        },
        // 覆盖默认 gray/slate:Tailwind 默认值偏蓝(#1f2937/#0f172a 等),深色模式下会残留蓝调
        // 统一映射到中性 zinc 色相,与 dark 色阶同一体系
        gray: {
          50: '#FAFAFA',
          100: '#F4F4F5',
          200: '#E4E4E7',
          300: '#D4D4D8',
          400: '#A1A1AA',
          500: '#71717A',
          600: '#52525B',
          700: '#3F3F46',
          800: '#27272A',
          900: '#18181B',
          950: '#09090B'
        },
        slate: {
          50: '#FAFAFA',
          100: '#F4F4F5',
          200: '#E4E4E7',
          300: '#D4D4D8',
          400: '#A1A1AA',
          500: '#71717A',
          600: '#52525B',
          700: '#3F3F46',
          800: '#27272A',
          900: '#18181B',
          950: '#09090B'
        },
        // 深色模式背景 - 成熟黑色系(zinc 中性色相),品牌蓝仅作强调色
        // 注意:950 比 900 略亮,历史上作为"提升面"(elevated surface)使用,保持该关系
        dark: {
          50: '#FAFAFA',
          100: '#F0F0F1',
          200: '#D9D9DE',
          300: '#A6A6AF',
          400: '#77777F',
          500: '#55555C',
          600: '#333338',
          700: '#29292E',
          800: '#1F1F23',
          900: '#121215',
          950: '#18181B'
        }
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
        glass: '0 8px 32px rgba(13, 42, 63, 0.08)',
        'glass-sm': '0 4px 16px rgba(13, 42, 63, 0.06)',
        glow: '0 0 20px rgba(0, 210, 255, 0.28)',
        'glow-lg': '0 0 40px rgba(18, 167, 232, 0.35)',
        card: '0 1px 3px rgba(13, 42, 63, 0.04), 0 1px 2px rgba(13, 42, 63, 0.06)',
        'card-hover': '0 8px 24px rgba(13, 42, 63, 0.1)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #00D2FF 0%, #0B8FD8 100%)',
        'gradient-dark': 'linear-gradient(135deg, #1F1F23 0%, #0F0F11 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(0, 210, 255, 0.14) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(139, 221, 248, 0.12) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(18, 167, 232, 0.1) 0px, transparent 50%)'
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
          '0%': { boxShadow: '0 0 20px rgba(0, 210, 255, 0.28)' },
          '100%': { boxShadow: '0 0 30px rgba(18, 167, 232, 0.4)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}
