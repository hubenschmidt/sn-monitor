# Mock Interview Script

Read each question aloud. Pause between questions to let the candidate respond.

---

## Phase 1 — Easy: [Two Sum (LC #1)](https://leetcode.com/problems/two-sum/)

> Given an array of integers `nums` and an integer `target`, return the indices of the two numbers that add up to `target`. You may assume each input has exactly one solution and you may not use the same element twice. For example, given `nums = [2,7,11,15]` and `target = 9`, return `[0,1]`.

> What is the time and space complexity of your solution?

---

## Phase 2 — Easy: [Valid Parentheses (LC #20)](https://leetcode.com/problems/valid-parentheses/)

> Given a string containing just the characters `(`, `)`, `{`, `}`, `[` and `]`, determine if the input string is valid. An input string is valid if open brackets are closed by the same type of bracket and in the correct order. Every close bracket has a corresponding open bracket of the same type.

---

## Phase 3 — Medium: [Longest Substring Without Repeating Characters (LC #3)](https://leetcode.com/problems/longest-substring-without-repeating-characters/)

> Given a string `s`, find the length of the longest substring without repeating characters. For example, given `"abcabcbb"` the answer is `3` because `"abc"` is the longest substring without repeating characters.

> Can you walk me through how your solution handles the input `"pwwkew"`?

---

## Phase 4 — Medium: [Merge Intervals (LC #56)](https://leetcode.com/problems/merge-intervals/)

> Given an array of intervals where `intervals[i] = [start, end]`, merge all overlapping intervals and return an array of the non-overlapping intervals that cover all the intervals in the input. For example, given `[[1,3],[2,6],[8,10],[15,18]]` the output should be `[[1,6],[8,10],[15,18]]`.

> What is the time complexity and why? Does your solution handle unsorted input?

---

## Phase 5 — Medium: [LRU Cache (LC #146)](https://leetcode.com/problems/lru-cache/)

> Design a data structure that follows the constraints of a Least Recently Used cache. Implement the `LRUCache` class with `get(key)` which returns the value if the key exists and -1 otherwise, and `put(key, value)` which updates or inserts the value. When the cache reaches capacity, it should evict the least recently used key before inserting. Both operations must run in O(1) time.

> Walk me through what happens when we do: `put(1,1)`, `put(2,2)`, `get(1)`, `put(3,3)` with capacity 2.

---

## Phase 6 — Hard: [Trapping Rain Water (LC #42)](https://leetcode.com/problems/trapping-rain-water/)

> Given `n` non-negative integers representing an elevation map where the width of each bar is 1, compute how much water it can trap after raining. For example, given `[0,1,0,2,1,0,1,3,2,1,2,1]` the answer is `6`.

> Can you solve it with O(1) space using the two-pointer approach?

---

## Phase 7 — Hard: [Median of Two Sorted Arrays (LC #4)](https://leetcode.com/problems/median-of-two-sorted-arrays/)

> Given two sorted arrays `nums1` and `nums2` of size `m` and `n`, return the median of the two sorted arrays. The overall run time complexity should be O(log(m+n)). For example, given `nums1 = [1,3]` and `nums2 = [2]`, the median is `2.0`.

> What makes the O(log(m+n)) solution tricky compared to the naive O(m+n) merge approach?

---

## Phase 8 — Final Review

> Pick whichever solution you think could be improved the most and give me a final optimized version with brief inline comments.
