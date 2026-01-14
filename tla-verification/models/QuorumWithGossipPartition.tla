--------------------------- MODULE QuorumWithGossipPartition ---------------------------
\* NexKV 元数据层的 Gossip + Quorum 协议建模 - 网络分区版本
\* 建模目标：验证协议在网络分区故障下的正确性
\* Phase 2 任务 T2.4 - 故障注入模型

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
\* 全局状态变量
\*=============================================================================

VARIABLES knowledge,
         decision,
         version,
         network_status,
         partition_map

\*=============================================================================
\* 类型不变量
\*=============================================================================

TypeOK ==
    /\ knowledge \in [Nodes -> Knowledge]
    /\ decision \in [Nodes -> DecisionState]
    /\ version \in Nat
    /\ network_status \in {"normal", "partitioned"}
    /\ partition_map \in [Nodes -> SUBSET Nodes]

\*=============================================================================
\* 辅助定义
\*=============================================================================

AllDecided ==
    \A n \in Nodes : decision[n] # "undecided"

AllCommitted ==
    \A n \in Nodes : decision[n] = "committed"

VersionConstraint == version <= 2

\*=============================================================================
\* 初始化
\*=============================================================================

Init ==
    /\ knowledge = [n \in Nodes |-> [seen |-> {}, version |-> 0, decided |-> {}]]
    /\ decision = [n \in Nodes |-> "undecided"]
    /\ version = 0
    /\ network_status = "normal"
    /\ partition_map = [n \in Nodes |-> Nodes \ {n}]

\*=============================================================================
\* 网络分区动作
\*=============================================================================

NetworkPartition(partition1, partition2) ==
    /\ network_status = "normal"
    /\ partition1 \cup partition2 = Nodes
    /\ partition1 \cap partition2 = {}
    /\ partition1 # {} /\ partition2 # {}
    /\ network_status' = "partitioned"
    /\ partition_map' = [n \in Nodes |->
        IF n \in partition1 THEN partition1 \ {n}
        ELSE partition2 \ {n}]
    /\ UNCHANGED <<knowledge, decision, version>>

NetworkHeal ==
    /\ network_status = "partitioned"
    /\ network_status' = "normal"
    /\ partition_map' = [n \in Nodes |-> Nodes \ {n}]
    /\ UNCHANGED <<knowledge, decision, version>>

\*=============================================================================
\* Gossip 协议动作
\*=============================================================================

ProposeVote(n, v) ==
    /\ version = v
    /\ decision[n] = "undecided"
    /\ knowledge[n].version = v
    /\ knowledge' = [knowledge EXCEPT ![n].seen = @ \cup {n}]
    /\ UNCHANGED <<decision, version, network_status, partition_map>>

GossipExchange(p, q) ==
    /\ p # q
    /\ q \in partition_map[p]
    /\ knowledge[p].version = knowledge[q].version
    /\ LET newSeen == knowledge[p].seen \cup knowledge[q].seen
           newDecided == knowledge[p].decided \cup knowledge[q].decided
       IN  knowledge' = [knowledge EXCEPT
                           ![p].seen = newSeen,
                           ![q].seen = newSeen,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<decision, version, network_status, partition_map>>

\*=============================================================================
\* Quorum 决策动作
\*=============================================================================

DecideCommit(n) ==
    /\ decision[n] = "undecided"
    /\ n \in knowledge[n].seen
    /\ Cardinality(knowledge[n].seen) >= Majority
    /\ decision' = [decision EXCEPT ![n] = "committed"]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version, network_status, partition_map>>

FollowDecision(n) ==
    /\ decision[n] = "undecided"
    /\ \E d \in knowledge[n].decided :
        decision[d] # "undecided"
    /\ LET \* 获取做出决策的节点
        decidedNode == CHOOSE d \in knowledge[n].decided : decision[d] # "undecided"
        \* 获取该节点的决策值
        nodeDecision == decision[decidedNode]
       IN  decision' = [decision EXCEPT ![n] = nodeDecision]
    /\ knowledge' = [knowledge EXCEPT ![n].decided = @ \cup {n}]
    /\ UNCHANGED <<version, network_status, partition_map>>

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
    \/ NetworkPartition({"n1", "n2"}, {"n3"})
    \/ NetworkPartition({"n1"}, {"n2", "n3"})
    \/ NetworkHeal

Spec == Init /\ [][Next]_<<knowledge, decision, version, network_status, partition_map>>

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
\* 网络分区相关不变量
\*=============================================================================

PartitionSafety ==
    network_status = "partitioned" =>
        \/ \A n \in Nodes : decision[n] # "committed"
        \/ \E p \in {"normal", "partitioned"} :
            LET partitionedNodes == IF p = "normal" THEN Nodes
                                     ELSE {n \in Nodes : \E q \in partition_map[n] : TRUE}
            IN  Cardinality({n \in partitionedNodes : decision[n] = "committed"}) <= Majority

PartitionConsistency ==
    \A p1, p2 \in Nodes :
        p2 \in partition_map[p1] =>
            decision[p1] = decision[p2] \/ decision[p1] = "undecided" \/ decision[p2] = "undecided"

NetworkStatusValid ==
    network_status = "normal" =>
        \A n \in Nodes : partition_map[n] = Nodes \ {n}

===========================================================
END
===========================================================
