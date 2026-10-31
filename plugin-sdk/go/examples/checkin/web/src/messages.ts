// messages.ts supplies the example's English original and Simplified Chinese
// translation. Host language selection chooses the active dictionary, while
// components keep all user-facing labels and recovery messages outside their
// request handling and rendering decisions.

// The two dictionaries share keys so changing host language never changes the
// example's actions or hides a loading, completion, or recovery state.
export const messages = {
  en: {
    title: 'Daily check-in',
    description: 'Claim your daily balance reward.',
    reward: 'Today’s reward',
    claim: 'Claim reward',
    claiming: 'Claiming…',
    completed: 'You have checked in today.',
    next: 'Come back tomorrow for your next reward.',
    received: 'Reward received',
    failed: 'Could not complete check-in. Try again.',
    loadFailed: 'Could not load check-in status.',
    retry: 'Try again',
    refreshFailed: 'Your reward was received, but the displayed balance could not refresh.',
    refreshBalance: 'Refresh balance',
    history: 'Check-in history',
    historyDescription: 'Rewards recorded by this plugin.',
    user: 'User',
    day: 'Date',
    amount: 'Reward',
    status: 'Status',
    done: 'Completed',
    pending: 'Pending',
    empty: 'No check-ins yet',
    more: 'Load more',
  },
  zh: {
    title: '每日签到',
    description: '每天签到领取余额奖励。',
    reward: '今日奖励',
    claim: '签到领取',
    claiming: '领取中…',
    completed: '今日已签到',
    next: '明天再来领取下一份奖励。',
    received: '奖励已到账',
    failed: '签到未完成，请重试。',
    loadFailed: '无法加载签到状态。',
    retry: '重试',
    refreshFailed: '奖励已到账，但页面余额未能刷新。',
    refreshBalance: '刷新余额',
    history: '签到记录',
    historyDescription: '此插件发放的奖励记录。',
    user: '用户',
    day: '日期',
    amount: '奖励',
    status: '状态',
    done: '已完成',
    pending: '待完成',
    empty: '暂无签到记录',
    more: '加载更多',
  },
}
