--------------------------- MODULE QuorumWithGossip ---------------------------
\* NexKV 元数据层的 Gossip + Quorum 协议建模
\* 建模目标：验证信息传播和决策一致性的交互

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NULL
ASSUME NULL \notin {"n1", "n2", "n3"}

\* 节点集合
Nodes == {"n1", "n2", "n3"}
Majority == 2

\* 节点的决策状态（简化：只有 commit 和 undecided）
DecisionState == {"undecided", "committed"}

\* 每个节点的知识（通过 gossip 收集的信息）
Knowledge == [seen: SUBSET Nodes,           \* 这个节点知道哪些节点已经 ACK
             version: Nat,                  \* 当前投票的版本号
             decided: SUBSET Nodes]         \* 这个节点知道哪些节点已经做出决策

\* 全局状态变量
VARIABLES knowledge,  \* 每个节点的知识集: [node -> Knowledge]
         decision,   \* 每个节点的决策: [node -> DecisionState]
         version     \* 全局投票版本号

\* 类型不变量
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

\* 发起投票：节点 n 对版本 v 发起投票
\* 效果：n 将自己的 ACK 加入自己的知识集
ProposeVote(n, v) ==
    /\ version = v
    /\ decision[n] = "undecided"
    /\ knowledge[n].version = v
    /\ knowledge' = [knowledge EXCEPT ![n].seen = @ \cup {n}]
    /\ UNCHANGED <<decision, version>>

\* Gossip 交换：节点 p 选择节点 q，交换各自的知识
\* 效果：两者的 knowledge 取并集（学到新的信息和决策）
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

\* 检查是否达到 quorum：如果节点 n 知道的已 ACK 节点数 >= Majority，
\* 则 n 可以决定 commit
\* 要求：n 必须先给自己投票（n 在自己的 seen 集合中）
DecideCommit(n) ==
    /\ decision[n] = "undecided"
    /\ n \in knowledge[n].seen              \* 节点必须先给自己投票
    /\ Cardinality(knowledge[n].seen) >= Majority
    /\ decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version>>

\* 回滚：在实际系统中，超时由上层处理
\* 这里简化为：只有 commit 和 undecided 两种状态
\* DecideRollback(n) == ...

\* 跟随决策：如果节点 n 通过 gossip 知道其他节点已经 decided，
\* 它必须跟随同样的决策（防止脑裂）
FollowDecision(n) ==
    /\ decision[n] = "undecided"
    /\ \E d \in knowledge[n].decided :
        decision[d] # "undecided"
    /\ LET otherDecision == CHOOSE d \in knowledge[n].decided : decision[d] # "undecided"
       IN  decision' = [decision EXCEPT ![n] = otherDecision]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version>>

\*=============================================================================
\* 系统演化
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

Spec == Init /\ [][Next]_<<knowledge, decision, version>>

\*=============================================================================
\* 安全属性（不变量）
\*=============================================================================

\* 决策安全性：所有 committed 节点都基于 quorum 知识做决策
\*（确保没有节点在信息不足时错误地 commit）
DecisionSafety ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        Cardinality(knowledge[n].seen) >= Majority

\* 版本一致性：所有节点的 knowledge 版本必须一致
VersionConsistency ==
    \A n1, n2 \in Nodes :
        knowledge[n1].version = knowledge[n2].version

\* 决策传播一致性：如果两个节点都知道彼此的决策状态，
\* 它们看到的决策图应该一致（防止部分节点看到冲突的决策）
DecisionPropagationConsistency ==
    \A n1, n2 \in Nodes :
        (n1 \in knowledge[n2].decided /\ n2 \in knowledge[n1].decided) =>
        decision[n1] = decision[n2] \/ (decision[n1] # "undecided" /\ decision[n2] # "undecided")

\* 已决策节点的知识完整性：如果一个节点已经 commit，
\* 它的知识集合中必须包含至少 Majority 个节点（包括自己）
CommittedNodeKnowledgeIntegrity ==
    \A n \in Nodes :
        decision[n] = "committed" =>
        n \in knowledge[n].seen /\ Cardinality(knowledge[n].seen) >= Majority

\*=============================================================================
\* 辅助定义
\*=============================================================================

\* 检查是否所有节点都达成一致决策
AllDecided ==
    \A n \in Nodes : decision[n] # "undecided"

\* 检查是否所有节点都 committed
AllCommitted ==
    \A n \in Nodes : decision[n] = "committed"

\* 检查是否所有节点都 rolledback
AllRolledback ==
    \A n \in Nodes : decision[n] = "rolledback"

\*=============================================================================
\* 约束条件（用于状态空间限制）
\*=============================================================================

VersionConstraint == version <= 3
\* 限制版本号以防止状态爆炸

===========================================================
END
===========================================================
