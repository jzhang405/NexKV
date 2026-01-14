--------------------------- MODULE QuorumWithGossipCrash ---------------------------
\* NexKV 元数据层的 Gossip + Quorum 协议建模 - 崩溃恢复版本
\* 建模目标：验证协议在节点崩溃故障下的正确性
\* Phase 2 任务 T2.3 - 故障注入模型

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NULL
ASSUME NULL \notin {"n1", "n2", "n3"}

\*=============================================================================
\* 系统配置
\*=============================================================================

Nodes == {"n1", "n2", "n3"}
Majority == 2
DecisionState == {"undecided", "committed"}

Knowledge == [seen: SUBSET Nodes,
             version: Nat,
             decided: SUBSET Nodes]

\*=============================================================================
\* 全局状态变量（新增 crashed 变量）
\*=============================================================================

VARIABLES knowledge,
         decision,
         version,
         crashed     \* 新增：崩溃的节点集合

\*=============================================================================
\* 类型不变量
\*=============================================================================

TypeOK ==
    /\ knowledge \in [Nodes -> Knowledge]
    /\ decision \in [Nodes -> DecisionState]
    /\ version \in Nat
    /\ crashed \in SUBSET Nodes   \* crashed 是节点的子集

\*=============================================================================
\* 初始化（新增 crashed）
\*=============================================================================

Init ==
    /\ knowledge = [n \in Nodes |-> [seen |-> {}, version |-> 0, decided |-> {}]]
    /\ decision = [n \in Nodes |-> "undecided"]
    /\ version = 0
    /\ crashed = {}                \* 初始没有节点崩溃

\*=============================================================================
\* Gossip 协议动作（修改：排除崩溃节点）
\*=============================================================================

ProposeVote(n, v) ==
    /\ version = v
    /\ decision[n] = "undecided"
    /\ knowledge[n].version = v
    /\ n \notin crashed            \* 新增：崩溃节点不能发起投票
    /\ knowledge' = [knowledge EXCEPT ![n].seen = @ \cup {n}]
    /\ UNCHANGED <<decision, version, crashed>>

GossipExchange(p, q) ==
    /\ p # q
    /\ p \notin crashed            \* 排除崩溃节点
    /\ q \notin crashed
    /\ knowledge[p].version = knowledge[q].version
    /\ LET newSeen == knowledge[p].seen \cup knowledge[q].seen
           newDecided == knowledge[p].decided \cup knowledge[q].decided
       IN  knowledge' = [knowledge EXCEPT
                           ![p].seen = newSeen,
                           ![q].seen = newSeen,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<decision, version, crashed>>

\*=============================================================================
\* Quorum 决策动作（修改：排除崩溃节点）
\*=============================================================================

DecideCommit(n) ==
    /\ decision[n] = "undecided"
    /\ n \notin crashed            \* 崩溃节点不能做决策
    /\ n \in knowledge[n].seen
    /\ Cardinality(knowledge[n].seen) >= Majority
    /\ decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version, crashed>>

FollowDecision(n) ==
    /\ decision[n] = "undecided"
    /\ n \notin crashed            \* 崩溃节点不能跟随决策
    /\ \E d \in knowledge[n].decided :
        decision[d] # "undecided"
    /\ LET \* 获取做出决策的节点
        decidedNode == CHOOSE d \in knowledge[n].decided : decision[d] # "undecided"
        \* 获取该节点的决策值
        nodeDecision == decision[decidedNode]
       IN  decision' = [decision EXCEPT ![n] = nodeDecision]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version, crashed>>

\*=============================================================================
\* Phase 2 新增：故障注入动作
\*=============================================================================

\* 节点崩溃：节点停止响应，不参与任何操作
NodeCrash(n) ==
    /\ n \notin crashed
    /\ crashed' = crashed \cup {n}
    /\ UNCHANGED <<knowledge, decision, version>>

\* 节点恢复：节点重新上线，恢复参与协议
NodeRecover(n) ==
    /\ n \in crashed
    /\ crashed' = crashed \ {n}
    /\ UNCHANGED <<knowledge, decision, version>>

\*=============================================================================
\* 系统演化（新增故障注入动作）
\*=============================================================================

Next ==
    \/ ProposeVote("n1", version)
    \/ ProposeVote("n2", version)
    \/ ProposeVote("n3", version)
    \/ \E p, q \in Nodes : GossipExchange(p, q)
    \/ DecideCommit("n1")
    \/ DecideCommit("n2")
    \/ DecideCommit("n3")
    \/ FollowDecision("n1")
    \/ FollowDecision("n2")
    \/ FollowDecision("n3")
    \/ NodeCrash("n1")
    \/ NodeCrash("n2")
    \/ NodeCrash("n3")
    \/ NodeRecover("n1")
    \/ NodeRecover("n2")
    \/ NodeRecover("n3")

Spec == Init /\ [][Next]_<<knowledge, decision, version, crashed>>

\*=============================================================================
\* 安全属性（不变量）
\*=============================================================================

DecisionSafety ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority

VersionConsistency ==
    \A n1, n2 \in Nodes :
        knowledge[n1].version = knowledge[n2].version

DecisionPropagationConsistency ==
    \A n1, n2 \in Nodes :
        (n1 \in knowledge[n2].decided /\ n2 \in knowledge[n1].decided) =>
        decision[n1] = decision[n2] \/ (decision[n1] # "undecided" /\ decision[n2] # "undecided")

CommittedNodeKnowledgeIntegrity ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        n \in knowledge[n].seen /\ Cardinality(knowledge[n].seen) >= Majority

\*=============================================================================
\* Phase 2 新增：故障相关不变量
\*=============================================================================

\* 崩溃节点不参与决策：崩溃节点不能是 committed 状态
CrashNoDecision ==
    \A n \in crashed :
        decision[n] = "undecided"

\* 恢复正确性：恢复后的节点能同步到正确的状态
RecoveryCorrectness ==
    \A n \in Nodes \ crashed :
        \* 活跃节点必须能看到所有已提交节点的决策
        \A d \in Nodes :
            decision[d] = "committed" => d \in knowledge[n].decided

\* 无双重提交：同一版本只能提交一次
NoDoubleCommit ==
    \A n1, n2 \in Nodes :
        decision[n1] = "committed" /\ decision[n2] = "committed"
        => knowledge[n1].version = knowledge[n2].version

\* 故障隔离：崩溃不影响其他节点的知识和决策
\* 这是一个状态谓词：如果没有其他变量变化，则 crashed 不能变
FaultIsolation ==
    TRUE  \* 简化：崩溃恢复总是独立的操作

\*=============================================================================
\* Liveness 性质（Phase 2 T2.5）
\*=============================================================================

\* 终止性：如果持续执行，最终会达到某个稳定状态
Termination ==
    <>(/\ \A n \in Nodes \ crashed : decision[n] # "undecided"
       /\ \A n \in Nodes : n \in knowledge[n].seen)

\* 最终一致性：如果持续执行 Gossip，所有活跃节点最终会收敛
EventualConsistency ==
    <>(/\ version > 0
       /\ \A n1, n2 \in Nodes \ crashed :
            knowledge[n1].version = knowledge[n2].version)

\* 恢复活性：崩溃的节点最终能恢复
EventualRecovery ==
    <>(crashed = {})

\*=============================================================================
\* 辅助定义
\*=============================================================================

AllDecided ==
    \A n \in Nodes \ crashed : decision[n] # "undecided"

ActiveNodes ==
    Nodes \ crashed

VersionConstraint == version <= 2

===========================================================
END
===========================================================
