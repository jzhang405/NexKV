--------------------------- MODULE QuorumWithGossip5Nodes ---------------------------
\* NexKV 元数据层的 Gossip + Quorum 协议建模 - 5节点版本
\* 建模目标：验证协议在更大规模集群下的正确性
\* Phase 2 任务 T2.1

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NULL
ASSUME NULL \notin {"n1", "n2", "n3", "n4", "n5"}

\*=============================================================================
\* 系统配置
\*=============================================================================

\* 节点集合（3节点 -> 5节点）
Nodes == {"n1", "n2", "n3", "n4", "n5"}

\* Quorum 阈值（3节点的2 -> 5节点的3）
\* 公式：floor(N/2) + 1 = floor(5/2) + 1 = 2 + 1 = 3
Majority == 3

\* 节点的决策状态
DecisionState == {"undecided", "committed"}

\* 每个节点的知识
Knowledge == [seen: SUBSET Nodes,
             version: Nat,
             decided: SUBSET Nodes]

\*=============================================================================
\* 全局状态变量
\*=============================================================================

VARIABLES knowledge,  \* 每个节点的知识集: [node -> Knowledge]
         decision,   \* 每个节点的决策: [node -> DecisionState]
         version     \* 全局投票版本号

\*=============================================================================
\* 类型不变量
\*=============================================================================

TypeOK ==
    /\ knowledge \in [Nodes -> Knowledge]
    /\ decision \in [Nodes -> DecisionState]
    /\ version \in Nat

\*=============================================================================
\* 初始化
\*=============================================================================

Init ==
    /\ knowledge = [n \in Nodes |-> [seen |-> {}, version |-> 0, decided |-> {}]]
    /\ decision = [n \in Nodes |-> "undecided"]
    /\ version = 0

\*=============================================================================
\* Gossip 协议动作
\*=============================================================================

\* 发起投票
ProposeVote(n, v) ==
    /\ version = v
    /\ decision[n] = "undecided"
    /\ knowledge[n].version = v
    /\ knowledge' = [knowledge EXCEPT ![n].seen = @ \cup {n}]
    /\ UNCHANGED <<decision, version>>

\* Gossip 交换
GossipExchange(p, q) ==
    /\ p # q
    /\ knowledge[p].version = knowledge[q].version
    /\ LET newSeen == knowledge[p].seen \cup knowledge[q].seen
           newDecided == knowledge[p].decided \cup knowledge[q].decided
       IN  knowledge' = [knowledge EXCEPT
                           ![p].seen = newSeen,
                           ![q].seen = newSeen,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<decision, version>>

\*=============================================================================
\* Quorum 决策动作
\*=============================================================================

\* 决策提交（3节点的2 -> 5节点的3）
\* 修复：只将自己添加到 decided 集合，而不是所有 seen 节点
DecideCommit(n) ==
    /\ decision[n] = "undecided"
    /\ n \in knowledge[n].seen
    /\ Cardinality(knowledge[n].seen) >= Majority
    /\ decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]  \* 只添加自己
    /\ UNCHANGED <<version>>

\* 跟随决策（修复：要求节点先给自己投票，并只跟随 committed 决策）
FollowDecision(n) ==
    /\ decision[n] = "undecided"
    /\ n \in knowledge[n].seen  \* 节点必须先给自己投票
    /\ \E d \in knowledge[n].decided :
        decision[d] = "committed"
    /\ LET
        decidedNode == CHOOSE d \in knowledge[n].decided : decision[d] = "committed"
       IN  decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version>>

\*=============================================================================
\* 系统演化
\*=============================================================================

Next ==
    \/ ProposeVote("n1", version)
    \/ ProposeVote("n2", version)
    \/ ProposeVote("n3", version)
    \/ ProposeVote("n4", version)
    \/ ProposeVote("n5", version)
    \/ \E p, q \in Nodes : GossipExchange(p, q)
    \/ DecideCommit("n1")
    \/ DecideCommit("n2")
    \/ DecideCommit("n3")
    \/ DecideCommit("n4")
    \/ DecideCommit("n5")
    \/ FollowDecision("n1")
    \/ FollowDecision("n2")
    \/ FollowDecision("n3")
    \/ FollowDecision("n4")
    \/ FollowDecision("n5")

Spec == Init /\ [][Next]_<<knowledge, decision, version>>

\*=============================================================================
\* 安全属性（不变量）
\*=============================================================================

\* 决策安全性（Majority 2 -> 3）
DecisionSafety ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority

\* 版本一致性
VersionConsistency ==
    \A n1, n2 \in Nodes :
        knowledge[n1].version = knowledge[n2].version

\* 决策传播一致性
DecisionPropagationConsistency ==
    \A n1, n2 \in Nodes :
        (n1 \in knowledge[n2].decided /\ n2 \in knowledge[n1].decided) =>
        decision[n1] = decision[n2] \/ (decision[n1] # "undecided" /\ decision[n2] # "undecided")

\* 已决策节点的知识完整性
CommittedNodeKnowledgeIntegrity ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        n \in knowledge[n].seen /\ Cardinality(knowledge[n].seen) >= Majority

\*=============================================================================
\* Phase 2 新增性质
\*=============================================================================

\* 多数派覆盖：任何决策都有多数派支持
MajorityCoverage ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority

\* Quorum 可达性：这个性质不适用于初始状态，作为辅助定义
QuorumReachabilityHelper ==
    version > 0 =>  \* 只在有投票后才检查
        \A S \in SUBSET Nodes :
            Cardinality(S) >= Majority =>
            \E n \in Nodes :
                n \in S /\ n \in knowledge[n].seen

\*=============================================================================
\* 辅助定义
\*=============================================================================

AllDecided ==
    \A n \in Nodes : decision[n] # "undecided"

AllCommitted ==
    \A n \in Nodes : decision[n] = "committed"

AllRolledback ==
    \A n \in Nodes : decision[n] = "rolledback"

\*=============================================================================
\* 约束条件（状态空间限制）
\*=============================================================================

VersionConstraint == version <= 3

===========================================================
END
===========================================================
