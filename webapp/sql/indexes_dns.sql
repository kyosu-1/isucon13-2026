-- PowerDNS (gmysql) の records は name にインデックスが無い。水責め攻撃のたびに全件走査になる
CREATE INDEX records_name_type ON records (name, type);
