// 唯一桥接头：Swift 经 ObjC 头消费 Handoff-Mobile.framework 的 Bind* C 函数与 BindMachine 类。
// 为什么不是 import：modulemap 里模块名字面为 "Handoff-Mobile"（含连字符），Swift 无法 import。
#import <Handoff-Mobile/Handoff-Mobile.h>
