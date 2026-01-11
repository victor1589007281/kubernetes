# 算法模式与解题模板

## 目录
1. [双指针](#双指针)
2. [滑动窗口](#滑动窗口)
3. [二分查找](#二分查找)
4. [回溯算法](#回溯算法)
5. [动态规划](#动态规划)
6. [贪心算法](#贪心算法)
7. [分治算法](#分治算法)
8. [位运算](#位运算)

---

## 算法模式全景图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        LeetCode 算法模式分类                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                             算法模式                                         │
│               ┌───────────────┼───────────────┐                             │
│            搜索类           优化类          其他                             │
│         ┌────┼────┐      ┌────┼────┐     ┌──┼──┐                           │
│       双指针 滑窗 二分  贪心  DP  分治  回溯 位运算                          │
│         │    │    │      │    │    │     │    │                             │
│     相向/同向 定长/变长 左闭右开 局部最优 状态转移 递归 全排列 异或           │
│       快慢  子串/子数组 边界查找 证明正确 背包问题 归并 组合  与或           │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  选择策略:                                                       │       │
│   │  • 有序数组查找 → 二分                                           │       │
│   │  • 连续子数组/子串 → 滑动窗口                                    │       │
│   │  • 两数/三数之和 → 双指针                                        │       │
│   │  • 最优解 → DP/贪心                                              │       │
│   │  • 所有可能 → 回溯                                               │       │
│   │  • 分解子问题 → 分治                                             │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 双指针

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          双指针三种模式                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   1. 相向双指针 (对撞指针)                                                   │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [1, 2, 3, 4, 5, 6, 7]                                         │       │
│   │    L →              ← R                                          │       │
│   │                                                                  │       │
│   │   适用: 有序数组、回文判断、两数之和                             │       │
│   │   模板:                                                          │       │
│   │   for left < right {                                            │       │
│   │       if 条件满足 { 处理并返回 }                                │       │
│   │       else if 需要增大 { left++ }                               │       │
│   │       else { right-- }                                          │       │
│   │   }                                                              │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   2. 同向双指针 (快慢指针)                                                   │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [0, 1, 0, 3, 12]                                              │       │
│   │    S  F                                                          │       │
│   │    S     F                                                       │       │
│   │       S     F                                                    │       │
│   │                                                                  │       │
│   │   适用: 删除重复、移动零、链表找环                               │       │
│   │   模板:                                                          │       │
│   │   slow := 0                                                     │       │
│   │   for fast := 0; fast < n; fast++ {                             │       │
│   │       if 满足条件 {                                             │       │
│   │           nums[slow] = nums[fast]                               │       │
│   │           slow++                                                │       │
│   │       }                                                          │       │
│   │   }                                                              │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   3. 分离双指针 (两个数组)                                                   │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [1, 3, 5, 7]    [2, 4, 6, 8]                                  │       │
│   │    i               j                                             │       │
│   │                                                                  │       │
│   │   适用: 合并有序数组、交集                                       │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典题目模板

```go
// 两数之和 II (有序数组)
func twoSum(numbers []int, target int) []int {
    left, right := 0, len(numbers)-1
    for left < right {
        sum := numbers[left] + numbers[right]
        if sum == target {
            return []int{left + 1, right + 1}
        } else if sum < target {
            left++
        } else {
            right--
        }
    }
    return nil
}

// 移动零
func moveZeroes(nums []int) {
    slow := 0
    for fast := 0; fast < len(nums); fast++ {
        if nums[fast] != 0 {
            nums[slow], nums[fast] = nums[fast], nums[slow]
            slow++
        }
    }
}

// 验证回文串
func isPalindrome(s string) bool {
    left, right := 0, len(s)-1
    for left < right {
        for left < right && !isAlphaNum(s[left]) {
            left++
        }
        for left < right && !isAlphaNum(s[right]) {
            right--
        }
        if toLower(s[left]) != toLower(s[right]) {
            return false
        }
        left++
        right--
    }
    return true
}
```

---

## 滑动窗口

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          滑动窗口框架                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │                                                                  │       │
│   │   [a, b, c, d, e, f, g, h, i, j]                                │       │
│   │         [  窗口  ]                                              │       │
│   │         left   right                                             │       │
│   │                                                                  │       │
│   │   右指针: 扩展窗口                                               │       │
│   │   左指针: 收缩窗口                                               │       │
│   │                                                                  │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   通用模板:                                                                  │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   func slidingWindow(s string) {                                │       │
│   │       window := make(map[byte]int)                              │       │
│   │       left := 0                                                 │       │
│   │                                                                  │       │
│   │       for right := 0; right < len(s); right++ {                 │       │
│   │           c := s[right]                                         │       │
│   │           window[c]++  // 扩展窗口                              │       │
│   │                                                                  │       │
│   │           for 需要收缩窗口 {                                    │       │
│   │               d := s[left]                                      │       │
│   │               window[d]--  // 收缩窗口                          │       │
│   │               left++                                            │       │
│   │           }                                                      │       │
│   │                                                                  │       │
│   │           // 更新结果                                           │       │
│   │       }                                                          │       │
│   │   }                                                              │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   两类问题:                                                                  │
│   1. 固定窗口: 窗口大小固定，右移整个窗口                                    │
│   2. 可变窗口: 窗口大小变化，找最大/最小满足条件的窗口                       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典问题模板

```go
// 最小覆盖子串 (76)
func minWindow(s string, t string) string {
    need := make(map[byte]int)
    window := make(map[byte]int)
    for i := 0; i < len(t); i++ {
        need[t[i]]++
    }
    
    left, right := 0, 0
    valid := 0
    start, length := 0, len(s)+1
    
    for right < len(s) {
        c := s[right]
        right++
        // 扩展窗口
        if _, ok := need[c]; ok {
            window[c]++
            if window[c] == need[c] {
                valid++
            }
        }
        
        // 收缩窗口
        for valid == len(need) {
            if right-left < length {
                start = left
                length = right - left
            }
            d := s[left]
            left++
            if _, ok := need[d]; ok {
                if window[d] == need[d] {
                    valid--
                }
                window[d]--
            }
        }
    }
    
    if length == len(s)+1 {
        return ""
    }
    return s[start : start+length]
}

// 找所有字母异位词 (438)
func findAnagrams(s string, p string) []int {
    need := make(map[byte]int)
    window := make(map[byte]int)
    for i := 0; i < len(p); i++ {
        need[p[i]]++
    }
    
    left, right := 0, 0
    valid := 0
    result := []int{}
    
    for right < len(s) {
        c := s[right]
        right++
        if _, ok := need[c]; ok {
            window[c]++
            if window[c] == need[c] {
                valid++
            }
        }
        
        // 窗口大小等于 p 的长度时
        for right-left >= len(p) {
            if valid == len(need) {
                result = append(result, left)
            }
            d := s[left]
            left++
            if _, ok := need[d]; ok {
                if window[d] == need[d] {
                    valid--
                }
                window[d]--
            }
        }
    }
    return result
}
```

---

## 二分查找

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          二分查找三种变体                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   1. 标准二分 (找确切值)                                                     │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [1, 2, 3, 4, 5, 6, 7, 8, 9]  target = 5                       │       │
│   │    L           M           R                                     │       │
│   │             L  M  R          → 找到                              │       │
│   │                                                                  │       │
│   │   循环条件: left <= right                                       │       │
│   │   返回: mid 或 -1                                               │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   2. 左边界二分 (第一个 >= target 的位置)                                    │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [1, 2, 2, 2, 3]  target = 2                                   │       │
│   │    ↑                                                             │       │
│   │    找这个位置 (index = 1)                                        │       │
│   │                                                                  │       │
│   │   循环条件: left < right                                        │       │
│   │   收缩: nums[mid] >= target → right = mid                       │       │
│   │        nums[mid] < target  → left = mid + 1                     │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   3. 右边界二分 (最后一个 <= target 的位置)                                  │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │   [1, 2, 2, 2, 3]  target = 2                                   │       │
│   │             ↑                                                    │       │
│   │        找这个位置 (index = 3)                                    │       │
│   │                                                                  │       │
│   │   循环条件: left < right                                        │       │
│   │   收缩: nums[mid] > target  → right = mid                       │       │
│   │        nums[mid] <= target → left = mid + 1                     │       │
│   │   返回: left - 1                                                │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 模板代码

```go
// 标准二分
func binarySearch(nums []int, target int) int {
    left, right := 0, len(nums)-1
    for left <= right {
        mid := left + (right-left)/2
        if nums[mid] == target {
            return mid
        } else if nums[mid] < target {
            left = mid + 1
        } else {
            right = mid - 1
        }
    }
    return -1
}

// 左边界二分 (第一个 >= target)
func lowerBound(nums []int, target int) int {
    left, right := 0, len(nums)
    for left < right {
        mid := left + (right-left)/2
        if nums[mid] >= target {
            right = mid
        } else {
            left = mid + 1
        }
    }
    return left
}

// 右边界二分 (最后一个 <= target)
func upperBound(nums []int, target int) int {
    left, right := 0, len(nums)
    for left < right {
        mid := left + (right-left)/2
        if nums[mid] > target {
            right = mid
        } else {
            left = mid + 1
        }
    }
    return left - 1
}

// 搜索旋转排序数组 (33)
func search(nums []int, target int) int {
    left, right := 0, len(nums)-1
    for left <= right {
        mid := left + (right-left)/2
        if nums[mid] == target {
            return mid
        }
        // 判断哪半边有序
        if nums[left] <= nums[mid] { // 左半边有序
            if nums[left] <= target && target < nums[mid] {
                right = mid - 1
            } else {
                left = mid + 1
            }
        } else { // 右半边有序
            if nums[mid] < target && target <= nums[right] {
                left = mid + 1
            } else {
                right = mid - 1
            }
        }
    }
    return -1
}
```

---

## 回溯算法

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          回溯算法框架                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   回溯 = 决策树的深度优先遍历                                                │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │                                                                  │       │
│   │   func backtrack(路径, 选择列表) {                              │       │
│   │       if 满足结束条件 {                                         │       │
│   │           result.add(路径)                                      │       │
│   │           return                                                │       │
│   │       }                                                          │       │
│   │                                                                  │       │
│   │       for 选择 in 选择列表 {                                    │       │
│   │           做选择 (将选择加入路径)                               │       │
│   │           backtrack(路径, 选择列表)                             │       │
│   │           撤销选择 (将选择从路径移除)                           │       │
│   │       }                                                          │       │
│   │   }                                                              │       │
│   │                                                                  │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   三要素:                                                                    │
│   1. 路径: 已经做出的选择                                                    │
│   2. 选择列表: 当前可以做的选择                                              │
│   3. 结束条件: 到达决策树底层                                                │
│                                                                              │
│   问题分类:                                                                  │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  排列问题: 顺序相关，需要 visited 数组                          │       │
│   │  组合问题: 顺序无关，需要 start 参数                            │       │
│   │  子集问题: 组合的特例，每个节点都是解                           │       │
│   │  棋盘问题: 需要验证当前选择是否合法                             │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典问题模板

```go
// 全排列 (46)
func permute(nums []int) [][]int {
    result := [][]int{}
    used := make([]bool, len(nums))
    
    var backtrack func(path []int)
    backtrack = func(path []int) {
        if len(path) == len(nums) {
            temp := make([]int, len(path))
            copy(temp, path)
            result = append(result, temp)
            return
        }
        
        for i := 0; i < len(nums); i++ {
            if used[i] {
                continue
            }
            used[i] = true
            path = append(path, nums[i])
            backtrack(path)
            path = path[:len(path)-1]
            used[i] = false
        }
    }
    
    backtrack([]int{})
    return result
}

// 子集 (78)
func subsets(nums []int) [][]int {
    result := [][]int{}
    
    var backtrack func(start int, path []int)
    backtrack = func(start int, path []int) {
        // 每个节点都是一个解
        temp := make([]int, len(path))
        copy(temp, path)
        result = append(result, temp)
        
        for i := start; i < len(nums); i++ {
            path = append(path, nums[i])
            backtrack(i+1, path)
            path = path[:len(path)-1]
        }
    }
    
    backtrack(0, []int{})
    return result
}

// 组合总和 (39) - 元素可重复使用
func combinationSum(candidates []int, target int) [][]int {
    result := [][]int{}
    
    var backtrack func(start, sum int, path []int)
    backtrack = func(start, sum int, path []int) {
        if sum == target {
            temp := make([]int, len(path))
            copy(temp, path)
            result = append(result, temp)
            return
        }
        if sum > target {
            return
        }
        
        for i := start; i < len(candidates); i++ {
            path = append(path, candidates[i])
            backtrack(i, sum+candidates[i], path) // i 不加1，可重复使用
            path = path[:len(path)-1]
        }
    }
    
    backtrack(0, 0, []int{})
    return result
}

// N皇后 (51)
func solveNQueens(n int) [][]string {
    result := [][]string{}
    board := make([][]byte, n)
    for i := range board {
        board[i] = make([]byte, n)
        for j := range board[i] {
            board[i][j] = '.'
        }
    }
    
    var isValid func(row, col int) bool
    isValid = func(row, col int) bool {
        // 检查列
        for i := 0; i < row; i++ {
            if board[i][col] == 'Q' {
                return false
            }
        }
        // 检查左上对角线
        for i, j := row-1, col-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
            if board[i][j] == 'Q' {
                return false
            }
        }
        // 检查右上对角线
        for i, j := row-1, col+1; i >= 0 && j < n; i, j = i-1, j+1 {
            if board[i][j] == 'Q' {
                return false
            }
        }
        return true
    }
    
    var backtrack func(row int)
    backtrack = func(row int) {
        if row == n {
            temp := make([]string, n)
            for i := range board {
                temp[i] = string(board[i])
            }
            result = append(result, temp)
            return
        }
        
        for col := 0; col < n; col++ {
            if !isValid(row, col) {
                continue
            }
            board[row][col] = 'Q'
            backtrack(row + 1)
            board[row][col] = '.'
        }
    }
    
    backtrack(0)
    return result
}
```

---

## 动态规划

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          动态规划框架                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   DP 解题四步法:                                                             │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  1. 定义状态: dp[i] 或 dp[i][j] 代表什么                        │       │
│   │  2. 状态转移: dp[i] = f(dp[i-1], dp[i-2], ...)                 │       │
│   │  3. 初始条件: dp[0], dp[1] 等边界值                            │       │
│   │  4. 遍历顺序: 确保计算时依赖的状态已计算                        │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   常见 DP 类型:                                                              │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │                                                                  │       │
│   │  1. 线性 DP                                                      │       │
│   │     • 爬楼梯: dp[i] = dp[i-1] + dp[i-2]                         │       │
│   │     • 打家劫舍: dp[i] = max(dp[i-1], dp[i-2] + nums[i])         │       │
│   │     • 最长递增子序列: dp[i] = max(dp[j] + 1) for j < i          │       │
│   │                                                                  │       │
│   │  2. 二维 DP                                                      │       │
│   │     • 最小路径和: dp[i][j] = grid[i][j] + min(dp[i-1][j], dp[i][j-1])│
│   │     • 编辑距离: 三种操作取最小                                   │       │
│   │                                                                  │       │
│   │  3. 背包 DP                                                      │       │
│   │     • 01背包: dp[i][w] = max(不选, 选)                          │       │
│   │     • 完全背包: 物品可无限选                                     │       │
│   │                                                                  │       │
│   │  4. 区间 DP                                                      │       │
│   │     • 戳气球: dp[i][j] = max(dp[i][k] + dp[k][j] + ...)         │       │
│   │                                                                  │       │
│   │  5. 树形 DP                                                      │       │
│   │     • 打家劫舍III: 选/不选当前节点                               │       │
│   │                                                                  │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 背包问题详解

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          背包问题分类                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   1. 01背包 (每个物品只能选一次)                                             │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  dp[i][w] = 前i个物品，容量w的最大价值                          │       │
│   │                                                                  │       │
│   │  dp[i][w] = max(                                                │       │
│   │      dp[i-1][w],           // 不选第i个                         │       │
│   │      dp[i-1][w-wi] + vi    // 选第i个                           │       │
│   │  )                                                               │       │
│   │                                                                  │       │
│   │  空间优化 (一维):                                                │       │
│   │  for i := 0 to n:                                               │       │
│   │      for w := W to wi:     // 倒序遍历!                         │       │
│   │          dp[w] = max(dp[w], dp[w-wi] + vi)                      │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   2. 完全背包 (每个物品可以选无限次)                                         │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  dp[i][w] = max(                                                │       │
│   │      dp[i-1][w],           // 不选第i个                         │       │
│   │      dp[i][w-wi] + vi      // 选第i个 (注意是 i 不是 i-1)       │       │
│   │  )                                                               │       │
│   │                                                                  │       │
│   │  空间优化 (一维):                                                │       │
│   │  for i := 0 to n:                                               │       │
│   │      for w := wi to W:     // 正序遍历!                         │       │
│   │          dp[w] = max(dp[w], dp[w-wi] + vi)                      │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   题目变形:                                                                  │
│   • 求最大价值 → 经典背包                                                    │
│   • 求恰好装满 → 初始化 dp[0]=0, 其他=-∞                                    │
│   • 求方案数 → dp[i][w] += dp[i-1][w-wi]                                    │
│   • 求最少物品数 → dp[i][w] = min(dp[i-1][w], dp[i-1][w-wi] + 1)            │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典问题模板

```go
// 最长递增子序列 (300)
func lengthOfLIS(nums []int) int {
    n := len(nums)
    if n == 0 {
        return 0
    }
    
    // dp[i] = 以 nums[i] 结尾的 LIS 长度
    dp := make([]int, n)
    for i := range dp {
        dp[i] = 1
    }
    
    maxLen := 1
    for i := 1; i < n; i++ {
        for j := 0; j < i; j++ {
            if nums[j] < nums[i] {
                dp[i] = max(dp[i], dp[j]+1)
            }
        }
        maxLen = max(maxLen, dp[i])
    }
    return maxLen
}

// 编辑距离 (72)
func minDistance(word1 string, word2 string) int {
    m, n := len(word1), len(word2)
    // dp[i][j] = word1[0..i-1] 转换为 word2[0..j-1] 的最少操作数
    dp := make([][]int, m+1)
    for i := range dp {
        dp[i] = make([]int, n+1)
    }
    
    // 初始化
    for i := 0; i <= m; i++ {
        dp[i][0] = i
    }
    for j := 0; j <= n; j++ {
        dp[0][j] = j
    }
    
    for i := 1; i <= m; i++ {
        for j := 1; j <= n; j++ {
            if word1[i-1] == word2[j-1] {
                dp[i][j] = dp[i-1][j-1]
            } else {
                dp[i][j] = min(
                    dp[i-1][j]+1,   // 删除
                    dp[i][j-1]+1,   // 插入
                    dp[i-1][j-1]+1, // 替换
                )
            }
        }
    }
    return dp[m][n]
}

// 零钱兑换 (322) - 完全背包
func coinChange(coins []int, amount int) int {
    dp := make([]int, amount+1)
    for i := 1; i <= amount; i++ {
        dp[i] = amount + 1 // 无穷大
    }
    
    for i := 1; i <= amount; i++ {
        for _, coin := range coins {
            if coin <= i {
                dp[i] = min(dp[i], dp[i-coin]+1)
            }
        }
    }
    
    if dp[amount] > amount {
        return -1
    }
    return dp[amount]
}

// 分割等和子集 (416) - 01背包
func canPartition(nums []int) bool {
    sum := 0
    for _, num := range nums {
        sum += num
    }
    if sum%2 != 0 {
        return false
    }
    
    target := sum / 2
    dp := make([]bool, target+1)
    dp[0] = true
    
    for _, num := range nums {
        for j := target; j >= num; j-- { // 倒序
            dp[j] = dp[j] || dp[j-num]
        }
    }
    return dp[target]
}
```

---

## 贪心算法

### 核心思想

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          贪心算法框架                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   贪心 vs 动态规划:                                                          │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  贪心: 每一步选择局部最优，期望得到全局最优                      │       │
│   │  DP:   考虑所有子问题，确保得到全局最优                          │       │
│   │                                                                  │       │
│   │  贪心适用条件:                                                   │       │
│   │  1. 贪心选择性质: 局部最优能导出全局最优                         │       │
│   │  2. 最优子结构: 问题的最优解包含子问题的最优解                   │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   常见贪心问题:                                                              │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  • 区间问题: 按起点/终点排序，选择最优区间                       │       │
│   │  • 跳跃问题: 每步跳到能到达的最远位置                            │       │
│   │  • 分配问题: 优先满足最容易满足的                                │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典问题

```go
// 跳跃游戏 (55)
func canJump(nums []int) bool {
    maxReach := 0
    for i := 0; i <= maxReach && i < len(nums); i++ {
        if i+nums[i] > maxReach {
            maxReach = i + nums[i]
        }
    }
    return maxReach >= len(nums)-1
}

// 跳跃游戏 II (45)
func jump(nums []int) int {
    jumps := 0
    end := 0      // 当前跳跃能到达的边界
    maxReach := 0 // 下一步能到达的最远位置
    
    for i := 0; i < len(nums)-1; i++ {
        if i+nums[i] > maxReach {
            maxReach = i + nums[i]
        }
        if i == end {
            jumps++
            end = maxReach
        }
    }
    return jumps
}

// 无重叠区间 (435)
func eraseOverlapIntervals(intervals [][]int) int {
    if len(intervals) == 0 {
        return 0
    }
    
    // 按结束时间排序
    sort.Slice(intervals, func(i, j int) bool {
        return intervals[i][1] < intervals[j][1]
    })
    
    count := 1  // 不重叠的区间数
    end := intervals[0][1]
    
    for i := 1; i < len(intervals); i++ {
        if intervals[i][0] >= end {
            count++
            end = intervals[i][1]
        }
    }
    
    return len(intervals) - count
}
```

---

## 位运算

### 常用技巧

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          位运算常用技巧                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   基本操作:                                                                  │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  n & 1        获取最低位                                        │       │
│   │  n >> 1       右移一位 (除以2)                                  │       │
│   │  n << 1       左移一位 (乘以2)                                  │       │
│   │  n & (n-1)    消除最低位的1                                     │       │
│   │  n | (n+1)    将最低位的0变为1                                  │       │
│   │  n & -n       获取最低位的1                                     │       │
│   │  n ^ n = 0    相同数异或为0                                     │       │
│   │  n ^ 0 = n    任何数与0异或不变                                 │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
│   常见应用:                                                                  │
│   ┌─────────────────────────────────────────────────────────────────┐       │
│   │  • 判断奇偶: n & 1 == 0 为偶数                                  │       │
│   │  • 交换两数: a ^= b; b ^= a; a ^= b                             │       │
│   │  • 判断2的幂: n > 0 && n & (n-1) == 0                           │       │
│   │  • 统计1的个数: 循环 n &= (n-1) 并计数                          │       │
│   │  • 找唯一的数: 所有数异或，结果就是唯一的数                      │       │
│   └─────────────────────────────────────────────────────────────────┘       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 经典问题

```go
// 只出现一次的数字 (136)
func singleNumber(nums []int) int {
    result := 0
    for _, num := range nums {
        result ^= num
    }
    return result
}

// 汉明距离 (461)
func hammingDistance(x int, y int) int {
    xor := x ^ y
    count := 0
    for xor != 0 {
        count++
        xor &= xor - 1
    }
    return count
}

// 比特位计数 (338)
func countBits(n int) []int {
    result := make([]int, n+1)
    for i := 1; i <= n; i++ {
        // i 的 1 的个数 = i/2 的 1 的个数 + i 的最低位
        result[i] = result[i>>1] + (i & 1)
    }
    return result
}
```

---

## 算法选择决策树

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          如何选择算法                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   问题类型                           推荐算法                                │
│   ────────────────────────────────   ──────────────────────                 │
│   有序数组查找                       二分查找                                │
│   连续子数组/子串                    滑动窗口                                │
│   两数之和/三数之和                  双指针                                  │
│   所有排列/组合/子集                 回溯                                    │
│   最优解/计数/可行性                 动态规划                                │
│   区间选择/任务调度                  贪心                                    │
│   大规模相同子问题                   分治                                    │
│   图的连通性                         并查集/DFS/BFS                          │
│   最短路径                           BFS/Dijkstra                            │
│   拓扑依赖                           拓扑排序                                │
│   下一个更大/更小元素                单调栈                                  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

